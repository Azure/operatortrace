#!/bin/sh
set -o errexit

CLUSTER_NAME="kubetracer-demo"
reg_name='kind-registry'
reg_port='5001'

cluster_exists() {
    kind get clusters | grep -q "^${CLUSTER_NAME}$"
}

echo "Checking if kind cluster '${CLUSTER_NAME}' exists..."
if cluster_exists; then
    echo "Kind cluster '${CLUSTER_NAME}' already exists. Skipping cluster creation."
    CONTEXT="kind-${CLUSTER_NAME}"
    echo "Setting kubectl context to ${CONTEXT}..."
    kubectl config use-context "${CONTEXT}"

    echo "Applying manifests..."
    kubectl apply -f k8s/manifests.yaml

    echo "Building and pushing Docker image..."
    docker build -t localhost:${reg_port}/sample-operator:latest -f sample-operator/Dockerfile ../
    docker push localhost:${reg_port}/sample-operator:latest


    echo "Deleting existing sample-operator deployment... if it exists"
    kubectl -n monitoring delete deployment sample-operator --ignore-not-found 

    cd sample-operator && make manifests && make install && make deploy

    kubectl apply -f config/samples/app_v1_sample.yaml

    exit 0
fi

# 1. Create registry container unless it already exists
if [ "$(docker inspect -f '{{.State.Running}}' "${reg_name}" 2>/dev/null || true)" != 'true' ]; then
  echo "Creating local registry container ${reg_name}:${reg_port}"
  docker run \
    -d --restart=always -p "127.0.0.1:${reg_port}:5000" --network bridge --name "${reg_name}" \
    registry:2
fi

# 2. Create kind cluster with containerd registry config dir enabled
echo "Creating kind cluster ${CLUSTER_NAME}..."
cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 32000    # NodePort as set in Service manifest for grafana
        hostPort: 32000
        protocol: TCP
EOF

# 3. Add the registry config to the nodes
REGISTRY_DIR="/etc/containerd/certs.d/localhost:${reg_port}"
for node in $(kind get nodes --name "${CLUSTER_NAME}"); do
  docker exec "${node}" mkdir -p "${REGISTRY_DIR}"
  cat <<EOF | docker exec -i "${node}" cp /dev/stdin "${REGISTRY_DIR}/hosts.toml"
[host."http://${reg_name}:5000"]
EOF
done

# 4. Connect the registry to the cluster network if not already connected
if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${reg_name}")" = 'null' ]; then
  echo "Connecting registry container to kind network..."
  docker network connect "kind" "${reg_name}"
fi

# 5. Document the local registry
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${reg_port}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

# 6. Install cert manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml

# 7. Wait for cert manager to be ready
echo "Waiting for cert-manager to be ready..."
while ! kubectl wait --for=condition=available --timeout=60s deployment/cert-manager deployment/cert-manager-webhook -n cert-manager; do
  echo "Waiting for cert-manager to be ready..."
  sleep 5
done

# 8. Install manifests
kubectl apply -f k8s/manifests.yaml

# 9. Build and push sample operator
docker build -t localhost:${reg_port}/sample-operator:latest -f sample-operator/Dockerfile ../
docker push localhost:${reg_port}/sample-operator:latest

# 10. Install the operator CRDs
echo "Installing operator CRDs using make install..."  
cd sample-operator && make manifests && make install && make deploy

# 11. add CRD to trigger reconciler
kubectl apply -f sample-operator/config/samples/app_v1_sample.yaml