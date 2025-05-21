# Example

## Setup the kind cluster

`./setup-kind-cluster.sh`

## build the image

`cd example/example-operator`
`docker build -t example-operator:latest .`
`docker tag example-operator:latest localhost:5001/example-operator:latest`
`docker push localhost:5001/example-operator:latest`

## deploy the operator and manifests

`cd example/k8s`
`kubectl apply -f manifests.yaml`
