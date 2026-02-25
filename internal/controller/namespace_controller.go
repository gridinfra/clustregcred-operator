package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	gridv1alpha1 "github.com/gridinfra/clustregcred-operator/api/v1alpha1"
)

const (
	// Rate limiter configuration
	baseDelay = 5 * time.Millisecond
	maxDelay  = 1000 * time.Second
)

// System namespaces that should be skipped
var systemNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
	"default":         false, // allow default namespace
}

// NamespaceReconciler reconciles a Namespace object
// It directly watches Namespace events and creates secrets immediately
type NamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Cache to track namespaces that have been processed without annotation
	// This avoids repeated cleanup queries for namespaces without our annotation
}

// isSystemNamespace checks if the namespace should be skipped
func isSystemNamespace(name string) bool {
	if skip, exists := systemNamespaces[name]; exists {
		return skip
	}
	// Skip namespaces starting with "kube-"
	return strings.HasPrefix(name, "kube-")
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=grid.maozi.io,resources=clustregcreds,verbs=get;list;watch

// parseClustRegCredNames parses the annotation value which can contain multiple
// ClustRegCred names separated by commas
// Example: "dockerhub-cred,ghcr-cred,ecr-cred"
func parseClustRegCredNames(annotation string) []string {
	if annotation == "" {
		return nil
	}

	var names []string
	for _, name := range strings.Split(annotation, ",") {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// Reconcile handles Namespace reconciliation
// This is triggered immediately when a namespace is created or updated
// Supports multiple ClustRegCreds in annotation: grid.maozi.io/clustreg: "cred1,cred2,cred3"
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Name)

	// Fast path: skip system namespaces
	if isSystemNamespace(req.Name) {
		return ctrl.Result{}, nil
	}

	// Fetch the Namespace
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, namespace); err != nil {
		if errors.IsNotFound(err) {
			// Namespace was deleted, secrets will be garbage collected
			logger.V(1).Info("Namespace not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Namespace")
		return ctrl.Result{}, err
	}

	// Skip terminating namespaces
	if namespace.Status.Phase == corev1.NamespaceTerminating {
		logger.V(1).Info("Namespace is terminating, skipping")
		return ctrl.Result{}, nil
	}

	// Check if namespace has the annotation
	annotationValue := ""
	if namespace.Annotations != nil {
		annotationValue = namespace.Annotations[AnnotationKey]
	}

	// Parse multiple ClustRegCred names from annotation
	clustRegCredNames := parseClustRegCredNames(annotationValue)

	// If annotation is removed or empty, clean up any managed secrets
	if len(clustRegCredNames) == 0 {
		// Only attempt cleanup if there might be secrets to clean
		// The predicate ensures we only get here on annotation removal
		if err := r.cleanupManagedSecrets(ctx, namespace.Name); err != nil {
			logger.Error(err, "Failed to cleanup managed secrets")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("Processing namespace with clustreg annotation",
		"clustRegCreds", clustRegCredNames,
		"count", len(clustRegCredNames))

	// Build a set of current ClustRegCred names for cleanup detection
	currentCredSet := make(map[string]bool)
	for _, name := range clustRegCredNames {
		currentCredSet[name] = true
	}

	// Clean up secrets for ClustRegCreds that are no longer in the annotation
	if err := r.cleanupRemovedClustRegCreds(ctx, namespace.Name, currentCredSet); err != nil {
		logger.Error(err, "Failed to cleanup removed ClustRegCreds")
		// Continue processing remaining creds even if cleanup fails
	}

	// Process each ClustRegCred
	var syncErrors []error
	for _, credName := range clustRegCredNames {
		// Fetch the ClustRegCred (uses cached client)
		clustRegCred := &gridv1alpha1.ClustRegCred{}
		if err := r.Get(ctx, types.NamespacedName{Name: credName}, clustRegCred); err != nil {
			if errors.IsNotFound(err) {
				logger.Info("ClustRegCred not found, skipping", "clustRegCred", credName)
				continue
			}
			logger.Error(err, "Failed to get ClustRegCred", "clustRegCred", credName)
			syncErrors = append(syncErrors, err)
			continue
		}

		// Sync secret to this namespace immediately
		if err := SyncSecretToNamespace(ctx, r.Client, clustRegCred, namespace.Name); err != nil {
			logger.Error(err, "Failed to sync secret to namespace", "clustRegCred", credName)
			syncErrors = append(syncErrors, err)
			continue
		}

		logger.V(1).Info("Successfully synced secret to namespace",
			"clustRegCred", credName,
			"secretName", clustRegCred.Spec.SecretName)
	}

	// Return error if any sync failed (will trigger retry)
	if len(syncErrors) > 0 {
		return ctrl.Result{}, fmt.Errorf("failed to sync %d ClustRegCred(s)", len(syncErrors))
	}

	return ctrl.Result{}, nil
}

// cleanupManagedSecrets removes all secrets managed by this operator in the given namespace
func (r *NamespaceReconciler) cleanupManagedSecrets(ctx context.Context, namespace string) error {
	logger := log.FromContext(ctx)

	// List all secrets with our management label using indexed query
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList,
		client.InNamespace(namespace),
		client.HasLabels{"grid.maozi.io/clustregcred"},
		client.Limit(100), // Limit results for safety
	); err != nil {
		return err
	}

	// Fast path: no secrets to clean
	if len(secretList.Items) == 0 {
		return nil
	}

	logger.Info("Cleaning up all managed secrets", "namespace", namespace, "count", len(secretList.Items))

	for i := range secretList.Items {
		secret := &secretList.Items[i]
		logger.V(1).Info("Deleting managed secret",
			"namespace", namespace,
			"secretName", secret.Name)
		if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// cleanupRemovedClustRegCreds removes secrets for ClustRegCreds that are no longer in the annotation
func (r *NamespaceReconciler) cleanupRemovedClustRegCreds(ctx context.Context, namespace string, currentCredSet map[string]bool) error {
	logger := log.FromContext(ctx)

	// List all secrets managed by this operator in the namespace
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList,
		client.InNamespace(namespace),
		client.HasLabels{"grid.maozi.io/clustregcred"},
		client.Limit(100),
	); err != nil {
		return err
	}

	for i := range secretList.Items {
		secret := &secretList.Items[i]
		credName := secret.Labels["grid.maozi.io/clustregcred"]

		// If this secret's ClustRegCred is not in the current set, delete it
		if !currentCredSet[credName] {
			logger.Info("Deleting secret for removed ClustRegCred",
				"namespace", namespace,
				"secretName", secret.Name,
				"clustRegCred", credName)
			if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// cleanupSecretsForClustRegCred removes secrets that reference a specific ClustRegCred
func (r *NamespaceReconciler) cleanupSecretsForClustRegCred(ctx context.Context, namespace, clustRegCredName string) error {
	logger := log.FromContext(ctx)

	// List secrets with specific ClustRegCred label
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList,
		client.InNamespace(namespace),
		client.MatchingLabels{"grid.maozi.io/clustregcred": clustRegCredName},
		client.Limit(100),
	); err != nil {
		return err
	}

	// Fast path: no secrets to clean
	if len(secretList.Items) == 0 {
		return nil
	}

	for i := range secretList.Items {
		secret := &secretList.Items[i]
		logger.Info("Deleting secret for non-existent ClustRegCred",
			"namespace", namespace,
			"secretName", secret.Name,
			"clustRegCred", clustRegCredName)
		if err := r.Delete(ctx, secret); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// clustRegAnnotationPredicate filters events to only process namespaces
// where our specific annotation has changed
type clustRegAnnotationPredicate struct {
	predicate.Funcs
}

func (p clustRegAnnotationPredicate) Create(e event.CreateEvent) bool {
	// Only process if the namespace has our annotation
	if e.Object == nil {
		return false
	}
	ns, ok := e.Object.(*corev1.Namespace)
	if !ok {
		return false
	}
	// Skip system namespaces
	if isSystemNamespace(ns.Name) {
		return false
	}
	// Process if annotation exists
	if ns.Annotations != nil {
		_, hasAnnotation := ns.Annotations[AnnotationKey]
		return hasAnnotation
	}
	return false
}

func (p clustRegAnnotationPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}
	nsOld, okOld := e.ObjectOld.(*corev1.Namespace)
	nsNew, okNew := e.ObjectNew.(*corev1.Namespace)
	if !okOld || !okNew {
		return false
	}
	// Skip system namespaces
	if isSystemNamespace(nsNew.Name) {
		return false
	}

	// Get old and new annotation values
	oldValue := ""
	newValue := ""
	if nsOld.Annotations != nil {
		oldValue = nsOld.Annotations[AnnotationKey]
	}
	if nsNew.Annotations != nil {
		newValue = nsNew.Annotations[AnnotationKey]
	}

	// Only trigger if our specific annotation changed
	return oldValue != newValue
}

func (p clustRegAnnotationPredicate) Delete(e event.DeleteEvent) bool {
	// No need to process deletes - secrets are garbage collected with namespace
	return false
}

func (p clustRegAnnotationPredicate) Generic(e event.GenericEvent) bool {
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(clustRegAnnotationPredicate{}).
		WithOptions(controller.Options{
			// Set max concurrent reconciles for better throughput
			MaxConcurrentReconciles: 5,
			// Use a rate limiter that's appropriate for namespace events
			//RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[ctrl.Request](
			//	baseDelay,
			//	maxDelay,
			//),
		}).
		Complete(r)
}
