package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gridv1alpha1 "github.com/gridinfra/clustregcred-operator/api/v1alpha1"
)

const (
	// AnnotationKey is the annotation key to look for on namespaces
	AnnotationKey = "grid.maozi.io/clustreg"

	// OwnerAnnotationKey marks which ClustRegCred owns this secret
	OwnerAnnotationKey = "grid.maozi.io/clustregcred-owner"
)

// ClustRegCredReconciler reconciles a ClustRegCred object
type ClustRegCredReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=grid.maozi.io,resources=clustregcreds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=grid.maozi.io,resources=clustregcreds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=grid.maozi.io,resources=clustregcreds/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles ClustRegCred reconciliation
func (r *ClustRegCredReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ClustRegCred instance
	clustRegCred := &gridv1alpha1.ClustRegCred{}
	err := r.Get(ctx, req.NamespacedName, clustRegCred)
	if err != nil {
		if errors.IsNotFound(err) {
			// ClustRegCred was deleted, cleanup handled by garbage collection
			logger.Info("ClustRegCred resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ClustRegCred")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling ClustRegCred", "name", clustRegCred.Name)

	// List all namespaces
	namespaceList := &corev1.NamespaceList{}
	if err := r.List(ctx, namespaceList); err != nil {
		logger.Error(err, "Failed to list namespaces")
		return ctrl.Result{}, err
	}

	syncedNamespaces := []string{}

	// Process each namespace
	for _, ns := range namespaceList.Items {
		// Check if namespace has the annotation
		if ns.Annotations == nil {
			continue
		}

		annotationValue, exists := ns.Annotations[AnnotationKey]
		if !exists {
			continue
		}

		// Check if this ClustRegCred is referenced in the annotation (supports comma-separated list)
		if !containsClustRegCred(annotationValue, clustRegCred.Name) {
			continue
		}

		// Skip terminating namespaces
		if ns.Status.Phase == corev1.NamespaceTerminating {
			continue
		}

		// Create or update the secret in this namespace
		if err := SyncSecretToNamespace(ctx, r.Client, clustRegCred, ns.Name); err != nil {
			logger.Error(err, "Failed to sync secret to namespace", "namespace", ns.Name)
			continue
		}

		syncedNamespaces = append(syncedNamespaces, ns.Name)
		logger.Info("Synced secret to namespace", "namespace", ns.Name, "secretName", clustRegCred.Spec.SecretName)
	}

	// Update status
	clustRegCred.Status.SyncedNamespaces = syncedNamespaces
	now := metav1.NewTime(time.Now())
	clustRegCred.Status.LastSyncTime = &now

	if err := r.Status().Update(ctx, clustRegCred); err != nil {
		logger.Error(err, "Failed to update ClustRegCred status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled ClustRegCred", "syncedNamespaces", len(syncedNamespaces))
	// No need to requeue frequently - NamespaceReconciler handles new namespaces immediately
	return ctrl.Result{RequeueAfter: 30 * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
// This controller only handles ClustRegCred changes (create/update/delete)
// Namespace events are handled by the NamespaceReconciler for immediate response
func (r *ClustRegCredReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gridv1alpha1.ClustRegCred{}).
		Complete(r)
}

// ============================================================================
// Shared utility functions used by both controllers
// ============================================================================

// containsClustRegCred checks if the annotation value contains the given ClustRegCred name
// Supports comma-separated list: "cred1,cred2,cred3"
func containsClustRegCred(annotationValue, credName string) bool {
	for _, name := range strings.Split(annotationValue, ",") {
		if strings.TrimSpace(name) == credName {
			return true
		}
	}
	return false
}

// SyncSecretToNamespace creates or updates the image pull secret in the specified namespace
// This is a shared function used by both ClustRegCredReconciler and NamespaceReconciler
func SyncSecretToNamespace(ctx context.Context, c client.Client, crc *gridv1alpha1.ClustRegCred, namespace string) error {
	logger := log.FromContext(ctx)

	// Generate docker config JSON
	dockerConfigJSON, err := GenerateDockerConfigJSON(crc.Spec.Registry, crc.Spec.Username, crc.Spec.Password, crc.Spec.Email)
	if err != nil {
		return fmt.Errorf("failed to generate docker config: %w", err)
	}

	// Define the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crc.Spec.SecretName,
			Namespace: namespace,
			Annotations: map[string]string{
				OwnerAnnotationKey: crc.Name,
			},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "clustregcred-operator",
				"grid.maozi.io/clustregcred":   crc.Name,
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: dockerConfigJSON,
		},
	}

	// Check if the secret already exists
	existingSecret := &corev1.Secret{}
	err = c.Get(ctx, types.NamespacedName{Name: crc.Spec.SecretName, Namespace: namespace}, existingSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the secret
			if err := c.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create secret: %w", err)
			}
			logger.Info("Created secret", "namespace", namespace, "secretName", crc.Spec.SecretName)
			return nil
		}
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Update the existing secret
	existingSecret.Data = secret.Data
	existingSecret.Annotations = secret.Annotations
	existingSecret.Labels = secret.Labels
	if err := c.Update(ctx, existingSecret); err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	logger.Info("Updated secret", "namespace", namespace, "secretName", crc.Spec.SecretName)

	return nil
}

// GenerateDockerConfigJSON generates the .dockerconfigjson content
func GenerateDockerConfigJSON(registry, username, password, email string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", username, password)))

	dockerConfig := map[string]interface{}{
		"auths": map[string]interface{}{
			registry: map[string]interface{}{
				"username": username,
				"password": password,
				"email":    email,
				"auth":     auth,
			},
		},
	}

	return json.Marshal(dockerConfig)
}
