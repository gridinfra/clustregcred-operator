# ClustRegCred Operator

一个 Kubernetes Operator，用于自动同步镜像拉取凭证（Image Pull Secret）到标记了特定注解的命名空间中。

## 功能特性

- **自定义资源 ClustRegCred**：集群级别的镜像仓库凭证资源
- **自动同步**：当命名空间包含 `grid.maozi.io/clustreg` 注解时，自动创建对应的镜像拉取 Secret
- **自动清理**：当命名空间的注解被移除时，自动删除对应的 Secret
- **实时监控**：监听命名空间创建/更新事件，毫秒级响应
- **状态追踪**：记录已同步的命名空间列表和最后同步时间
- **Helm Chart 支持**：提供完整的 Helm Chart 便于部署和管理

## 架构设计

采用**双 Controller 架构**，实现高性能的即时响应：

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Controller Manager                            │
├──────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌─────────────────────────┐     ┌─────────────────────────────────┐ │
│  │  ClustRegCredReconciler │     │     NamespaceReconciler         │ │
│  │  ───────────────────────│     │  ───────────────────────────────│ │
│  │  监听: ClustRegCred     │     │  监听: Namespace 创建/更新      │ │
│  │  触发: CR 创建/更新/删除│     │  触发: NS 创建或注解变更        │ │
│  │  职责: 全量同步所有 NS  │     │  职责: 即时创建单个 NS 的 Secret│ │
│  └───────────┬─────────────┘     └───────────────┬─────────────────┘ │
│              │                                   │                    │
│              └─────────────┬─────────────────────┘                    │
│                            ▼                                          │
│                 ┌─────────────────────┐                               │
│                 │  SyncSecretToNS()   │  共享同步逻辑                 │
│                 └─────────────────────┘                               │
│                            │                                          │
└────────────────────────────┼──────────────────────────────────────────┘
                             ▼
                   ┌──────────────────┐
                   │     Secret       │
                   │  (各命名空间)    │
                   └──────────────────┘
```

### 性能优化说明

| Controller | 触发时机 | 响应速度 | 职责 |
|------------|---------|---------|------|
| **NamespaceReconciler** | Namespace 创建/更新 | **即时** (~毫秒级) | 立即为新 NS 创建 Secret |
| **ClustRegCredReconciler** | ClustRegCred 变更 | 批量处理 | 全量同步、状态维护 |

- **即时响应**：`NamespaceReconciler` 直接监听 Namespace 事件，创建 NS 后立即触发 Secret 创建
- **全量同步**：`ClustRegCredReconciler` 负责 ClustRegCred 变更时的批量更新（如密码修改）
- **职责分离**：两个 Controller 独立运行，互不阻塞

## 快速开始

### 前置条件

- Kubernetes 集群 (v1.26+)
- kubectl 已配置
- Go 1.21+ (如需本地开发)

### 安装

1. **安装 CRD**

```bash
kubectl apply -f config/crd/bases/grid.maozi.io_clustregcreds.yaml
```

2. **部署 RBAC**

```bash
kubectl apply -f config/rbac/service_account.yaml
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/rbac/role_binding.yaml
```

3. **部署 Operator**

```bash
# 构建镜像
make docker-build IMG=your-registry/clustregcred-operator:v1.0.0

# 推送镜像
make docker-push IMG=your-registry/clustregcred-operator:v1.0.0

# 部署
kubectl apply -f config/manager/manager.yaml
```

或者使用一键部署：

```bash
make deploy IMG=your-registry/clustregcred-operator:v1.0.0
```

### 使用方式

1. **创建 ClustRegCred 资源**

```yaml
apiVersion: grid.maozi.io/v1alpha1
kind: ClustRegCred
metadata:
  name: dockerhub-cred
spec:
  registry: "https://index.docker.io/v1/"
  username: "your-username"
  password: "your-password"
  email: "your-email@example.com"
  secretName: "dockerhub-pull-secret"
```

```bash
kubectl apply -f config/samples/grid_v1alpha1_clustregcred.yaml
```

2. **创建带注解的命名空间（单个凭证）**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
  annotations:
    grid.maozi.io/clustreg: "dockerhub-cred"
```

创建后，Operator 会自动在 `my-app` 命名空间中创建名为 `dockerhub-pull-secret` 的镜像拉取 Secret。

**多个凭证示例：**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
  annotations:
    # 同时使用 Docker Hub 和 GitHub Container Registry
    grid.maozi.io/clustreg: "dockerhub-cred,ghcr-cred"
```

3. **移除 Secret（删除注解）**

如果要移除命名空间中的 Secret，只需删除对应的注解：

```bash
# 移除注解，Operator 会自动删除对应的 Secret
kubectl annotate namespace my-app grid.maozi.io/clustreg-
```

4. **验证 Secret 是否创建**

```bash
kubectl get secret dockerhub-pull-secret -n my-app
```

5. **在 Pod 中使用**

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: my-app
spec:
  containers:
    - name: my-container
      image: your-private-registry/image:tag
  imagePullSecrets:
    - name: dockerhub-pull-secret
```

### 查看状态

```bash
# 查看 ClustRegCred 状态
kubectl get clustregcreds
kubectl get crc  # 简称

# 查看详细信息
kubectl describe crc dockerhub-cred
```

## API 参考

### ClustRegCredSpec

| 字段 | 类型 | 必填 | 描述 |
|------|------|------|------|
| `registry` | string | 是 | 容器镜像仓库 URL |
| `username` | string | 是 | 仓库认证用户名 |
| `password` | string | 是 | 仓库认证密码 |
| `email` | string | 否 | 关联邮箱地址 |
| `secretName` | string | 是 | 生成的 Secret 名称 |

### ClustRegCredStatus

| 字段 | 类型 | 描述 |
|------|------|------|
| `syncedNamespaces` | []string | 已同步的命名空间列表 |
| `lastSyncTime` | Time | 最后同步时间 |
| `conditions` | []Condition | 状态条件 |

### 命名空间注解

| 注解 | 值 | 描述 |
|------|------|------|
| `grid.maozi.io/clustreg` | ClustRegCred 名称（支持多个，逗号分隔） | 指定要同步的 ClustRegCred 资源名称 |

#### 多 ClustRegCred 支持

命名空间可以引用多个 ClustRegCred，使用逗号分隔：

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
  annotations:
    # 同时同步多个镜像仓库凭证
    grid.maozi.io/clustreg: "dockerhub-cred,ghcr-cred,ecr-cred"
```

这将在 `my-app` 命名空间中创建三个独立的 image pull secret。

**特性说明：**
- 每个 ClustRegCred 创建独立的 Secret（由 `secretName` 字段决定）
- 从列表中移除某个 ClustRegCred 会自动删除对应的 Secret
- 支持动态增减：可随时修改注解添加或移除凭证

## 本地开发

```bash
# 安装依赖
go mod download

# 运行测试
make test

# 本地运行（需要 kubeconfig）
make run

# 生成 CRD
make manifests

# 生成 DeepCopy 代码
make generate
```

## 项目结构

```
clustregcred-operator/
├── api/
│   └── v1alpha1/
│       ├── clustregcred_types.go      # CRD 类型定义
│       ├── groupversion_info.go       # API 组版本信息
│       └── zz_generated.deepcopy.go   # 自动生成的 DeepCopy 方法
├── cmd/
│   └── main.go                        # 程序入口
├── config/
│   ├── crd/
│   │   └── bases/                     # CRD YAML
│   ├── manager/                       # Deployment 配置
│   ├── rbac/                          # RBAC 配置
│   └── samples/                       # 示例资源
├── internal/
│   └── controller/
│       ├── clustregcred_controller.go # ClustRegCred Controller (批量同步)
│       └── namespace_controller.go    # Namespace Controller (即时响应)
├── hack/
│   └── boilerplate.go.txt            # 代码生成模板
├── Dockerfile
├── Makefile
└── README.md
```

## 使用 Helm 部署

项目提供了完整的 Helm Chart，位于 `charts/clustregcred-operator/` 目录。

### 快速安装

```bash
helm install clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  --create-namespace
```

### 带预配置凭证安装

```bash
helm install clustregcred-operator ./charts/clustregcred-operator \
  --namespace clustregcred-system \
  --create-namespace \
  --set clustRegCreds[0].name=dockerhub-cred \
  --set clustRegCreds[0].registry="https://index.docker.io/v1/" \
  --set clustRegCreds[0].username=myuser \
  --set clustRegCreds[0].password=mypassword \
  --set clustRegCreds[0].secretName=dockerhub-pull-secret
```

### 卸载

```bash
helm uninstall clustregcred-operator --namespace clustregcred-system
```

更多 Helm 配置选项请参考 [charts/clustregcred-operator/README.md](charts/clustregcred-operator/README.md)。

## 生命周期管理

### Secret 创建
- 当创建带有 `grid.maozi.io/clustreg` 注解的 Namespace 时，立即创建 Secret
- 当向现有 Namespace 添加注解时，立即创建 Secret
- 当 ClustRegCred 资源更新（如密码变更）时，批量更新所有关联 Secret

### Secret 删除
- 当 Namespace 的 `grid.maozi.io/clustreg` 注解被移除时，自动删除对应 Secret
- 当 ClustRegCred 资源被删除时，不会主动删除已创建的 Secret（需手动清理或删除注解）
- 当 Namespace 被删除时，Secret 随 Namespace 一起被 Kubernetes 垃圾回收

## License

Apache License 2.0