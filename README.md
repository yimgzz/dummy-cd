# dummy-cd

## Description

simple helm & raw k8s resource manager based on git repos

dummy-cd contains kubernetes operator and gRPC server to manage helm or raw resources from git repositories

dummy-cd was written to learn k8s, helm, go-git api

## Local build and deploy

operator

```shell
cd operator 
make docker-build
make deploy # deploy to local minikube
```

server

```shell
cd server
make docker-build
make deploy # deploy to local minikube
```


## Custom Resource Examples

HTTPS Repository

```yaml
apiVersion: dummy.cd/v1alpha1
kind: Repository
metadata:
  labels:
    app.kubernetes.io/name: repository
    app.kubernetes.io/part-of: dummy.cd
  name: dummycd-hello-world-repo
spec:
  URL: "https://github.com/yimgzz/dummy-cd.git"
```


SSH Repository

```yaml
apiVersion: dummy.cd/v1alpha1
kind: Repository
metadata:
  labels:
    app.kubernetes.io/name: repository
    app.kubernetes.io/part-of: dummy.cd
  name: dummycd-hello-world-repo
spec:
  URL: "git@github.com:yimgzz/dummy-cd.git"
  privateKeySecret: "ssh-secret" # name of kubernetes secret with private ssh key in the same namespace
  insecureIgnoreHostKey: false
```

Helm Application

```yaml
apiVersion: dummy.cd/v1alpha1
kind: Application
metadata:
  labels:
    app.kubernetes.io/name: application
    app.kubernetes.io/part-of: dummy.cd
  name: dummycd-hello-world-helm-app
spec:
  URL: "https://github.com/yimgzz/dummy-cd.git"
  namespace: "dummycd-hello-world"
  reference: "main"
  sparsePath: "examples/helm-influxdb"
  helm:
    checkValuesEqual: true
    reInstallRelease: false
    createNamespace: true
    atomic: true
    valuesFiles:
      - my-influxdb-values.yaml
```

RAW K8s Resources

```yaml
apiVersion: dummy.cd/v1alpha1
kind: Application
metadata:
  labels:
    app.kubernetes.io/name: application
    app.kubernetes.io/part-of: dummy.cd
  name: dummycd-hello-world-raw-app
spec:
  URL: "https://github.com/yimgzz/dummy-cd.git"
  namespace: "dummycd-hello-world"
  reference: "main"
  sparsePath: "examples/raw-nginx"
```
