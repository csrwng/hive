#!/bin/bash

set -e

# Evaulate hive image using IMAGE_FORMAT
component=hive
HIVE_IMAGE=$(eval "echo $IMAGE_FORMAT")

# Release image
RELEASE_IMAGE="registry.svc.ci.openshift.org/${OPENSHIFT_BUILD_NAMESPACE}/release:latest"

ln -s $(which oc) $(pwd)/kubectl
export PATH=$PATH:$(pwd)

# download kustomize so we can use it for deploying
curl -O -L https://github.com/kubernetes-sigs/kustomize/releases/download/v2.0.0/kustomize_2.0.0_linux_amd64
mv kustomize_2.0.0_linux_amd64 kustomize
chmod u+x kustomize


# Install Hive
make deploy DEPLOY_IMAGE="${HIVE_IMAGE}"
