# ClustRegCred Operator Helm Chart

Helm Chart for deploying the ClustRegCred Operator on Kubernetes.

## Overview

ClustRegCred Operator automatically syncs image pull secrets to namespaces based on annotations. It provides:

- **Instant Response**: Creates secrets immediately when namespaces are created
- **Auto Cleanup**: Removes secrets when annotations are removed
- **Centralized Management**: Manage registry credentials in one place

## Prerequisites

- Kubernetes 1.26+
- Helm 3.x

## Installation

### Quick Install

```bash
helm install clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  --create-namespace
```

### Install with Custom Image

```bash
helm install clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  --create-namespace \
  --set image.repository=your-registry/clustregcred-operator \
  --set image.tag=v1.0.0
```

### Install with Pre-configured ClustRegCreds

```bash
helm install clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  --create-namespace \
  --set clustRegCreds[0].name=dockerhub-cred \
  --set clustRegCreds[0].registry="https://index.docker.io/v1/" \
  --set clustRegCreds[0].username=myuser \
  --set clustRegCreds[0].password=mypassword \
  --set clustRegCreds[0].email=myemail@example.com \
  --set clustRegCreds[0].secretName=dockerhub-pull-secret
```

## Configuration

### General Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of operator replicas | `1` |
| `image.repository` | Image repository | `clustregcred-operator` |
| `image.tag` | Image tag (default: appVersion) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Image pull secrets | `[]` |
| `nameOverride` | Override chart name | `""` |
| `fullnameOverride` | Override full name | `""` |

### ServiceAccount Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.annotations` | Service account annotations | `{}` |
| `serviceAccount.name` | Service account name | `""` |

### Resource Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `resources.requests.cpu` | CPU request | `10m` |
| `resources.requests.memory` | Memory request | `64Mi` |

### Operator Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `leaderElection.enabled` | Enable leader election | `true` |
| `metrics.enabled` | Enable metrics | `true` |
| `metrics.port` | Metrics port | `8080` |
| `healthProbe.port` | Health probe port | `8081` |
| `logging.development` | Enable development logging | `true` |

### ClustRegCreds Configuration

Pre-configure ClustRegCred resources in `values.yaml`:

```yaml
clustRegCreds:
  - name: dockerhub-cred
    registry: "https://index.docker.io/v1/"
    username: "your-username"
    password: "your-password"
    email: "your-email@example.com"
    secretName: "dockerhub-pull-secret"

  - name: ghcr-cred
    registry: "ghcr.io"
    username: "github-user"
    password: "ghp_xxxx"
    secretName: "ghcr-pull-secret"
```

## Usage

### Step 1: Create a ClustRegCred

```yaml
apiVersion: grid.maozi.io/v1alpha1
kind: ClustRegCred
metadata:
  name: my-registry-cred
spec:
  registry: "https://index.docker.io/v1/"
  username: "your-username"
  password: "your-password"
  email: "your-email@example.com"
  secretName: "my-pull-secret"
```

### Step 2: Add Annotation to Namespace

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
  annotations:
    grid.maozi.io/clustreg: "my-registry-cred"
```

The operator will **immediately** create `my-pull-secret` in the `my-app` namespace.

### Step 3: Use the Secret in Pods

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: my-app
spec:
  containers:
    - name: app
      image: your-private-registry/image:tag
  imagePullSecrets:
    - name: my-pull-secret
```

### Removing Secrets

To remove the synced secret from a namespace, simply remove the annotation:

```bash
kubectl annotate namespace my-app grid.maozi.io/clustreg-
```

The operator will automatically delete the managed secret.

## Upgrading

```bash
helm upgrade clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  -f my-values.yaml
```

## Uninstallation

```bash
# Uninstall the release
helm uninstall clustregcred-operator --namespace clustregcred-system

# Delete the namespace (optional)
kubectl delete namespace clustregcred-system
```

**Note:** CRDs are not deleted automatically. To remove them:

```bash
kubectl delete crd clustregcreds.grid.maozi.io
```

## Troubleshooting

### Check Operator Status

```bash
# Check pods
kubectl get pods -n clustregcred-system

# Check logs
kubectl logs -n clustregcred-system -l app.kubernetes.io/name=clustregcred-operator -f
```

### Check ClustRegCred Status

```bash
# List all ClustRegCreds
kubectl get clustregcreds
kubectl get crc  # short name

# Describe specific ClustRegCred
kubectl describe crc my-registry-cred
```

### Verify Secrets

```bash
# Check if secret exists in namespace
kubectl get secret my-pull-secret -n my-app

# List all managed secrets
kubectl get secrets -A -l app.kubernetes.io/managed-by=clustregcred-operator
```

### Common Issues

1. **Secret not created**: Verify the namespace has the correct annotation and ClustRegCred exists
2. **Wrong credentials**: Update the ClustRegCred, operator will sync changes automatically
3. **Secret not deleted**: Check operator logs for errors

## Architecture

The operator uses a dual-controller architecture:

- **NamespaceReconciler**: Watches namespace events for instant response
- **ClustRegCredReconciler**: Handles ClustRegCred changes for batch updates

This ensures:
- Millisecond-level response when namespaces are created
- Automatic secret cleanup when annotations are removed
- Batch updates when registry credentials change