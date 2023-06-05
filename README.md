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