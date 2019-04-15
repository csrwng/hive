/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package createcluster

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/hive/contrib/pkg/createcluster"
	"github.com/openshift/hive/contrib/pkg/installmanager"
	"github.com/openshift/hive/contrib/pkg/testresource"
	"github.com/openshift/hive/contrib/pkg/verification"
	"github.com/openshift/hive/pkg/imageset"

	"github.com/mitchellh/go-homedir"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
)

type CreateClusterOptions struct {
	Name               string
	Namespace          string
	LogLevel           string
	SSHKeyFile         string
	SSHKey             string
	BaseDomain         string
	PullSecret         string
	PullSecretFile     string
	AWSCredsFile       string
	ClusterImageSet    string
	HiveImage          string
	InstallerImage     string
	ReleaseImage       string
	UseClusterImageSet bool
	Output             string
	ManagedDNS         bool
	InfraMachineSet    bool
}

func NewCreateClusterCommand() *cobra.Command {
	opt := &CreateClusterOptions{}
	cmd := &cobra.Command{
		Use:   "create-cluster CLUSTER_DEPLOYMENT_NAME",
		Short: "Creates a new Hive cluster deployment",
		Run: func(cmd *cobra.Command, args []string) {
			if err := opt.Complete(cmd, args); err != nil {
				return
			}

			if err := opt.Validate(cmd); err != nil {
				return
			}

			opt.Run()
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&opt.Namespace, "namespace", "p", "", "Namespace to create cluster deployment in")
	flags.StringVar(&opt.LogLevel, "log-level", "info", "log level, one of: debug, info, warn, error, fatal, panic")
	flags.StringVar(&opt.SSHKeyFile, "ssh-key-file", "", "file name of SSH public key for cluster")
	flags.StringVar(&opt.SSHKey, "ssh-key", "", "SSH public key for cluster")
	flags.StringVar(&opt.BaseDomain, "base-domain", "new-installer.openshift.com", "Base domain for the cluster")
	flags.StringVar(&opt.PullSecret, "pull-secret", "", "Pull secret for cluster")
	flags.StringVar(&opt.PullSecretFile, "pull-secret-file", "", "Pull secret file for cluster")
	flags.StringVar(&opt.AWSCredsFile, "aws-creds-file", "", "AWS credentials file")
	flags.StringVar(&opt.ClusterImageSet, "cluster-image-set", "", "Cluster image set to use for this cluster deployment")
	flags.StringVar(&opt.HiveImage, "hive-image", "", "Hive image to use for installing/uninstalling this cluster deployment")
	flags.StringVar(&opt.InstallerImage, "installer-image", "", "Installer image to use for installing this cluster deployment")
	flags.StringVar(&opt.ReleaseImage, "release-image", "", "Release image to use for installing this cluster deployment")
	flags.StringVarP(&opt.Output, "output", "o", "", "Output of this command (nothing will be created on cluster). Valid values: yaml,json")
	flags.BoolVar(&opt.IncludeSecrets, "include-secrets", true, "Include secrets along with ClusterDeployment")
	return cmd
}

func (o *CreateClusterOptions) Complete(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		cmd.Usage()
		log.Info("You must specify a cluster deployment name")
		return fmt.Errorf("no cluster deployment specified")
	}
	o.Name = args[0]
}

func (o *CreateClusterOptions) Validate(cmd *cobra.Command) error {
	if len(o.SSHKey) == 0 && len(o.SSHKeyFile) == 0 && o.IncludeSecrets {
		log.Info("You must specify either --ssh-key or --ssh-key-file when creating a cluster with secrets")
		return fmt.Errorf("validation")
	}
	if len(o.SSHKey) > 0 && len(o.SSHKeyFile) > 0 {
		log.Info("You cannot specify both --ssh-key and --ssh-key-file")
		return fmt.Errorf("validation")
	}
	if len(o.PullSecret) > 0 && len(o.PullSecretFile) > 0 && o.IncludeSecrets {
		log.Info("You must specify either --pull-secret or --pull-secret-file when creating a cluster with secrets")
		return fmt.Errorf("validation")
	}
}

func (o *CreateClusterOptions) Run() {
	objs := o.GenerateObjects()
	if len(o.Output) > 0 {
		switch o.Output {
		case "yaml":
			writeYAML(objs)
		case "json":
			writeJSON(objs)
		}
		return
	}
	resource := o.GetResourceHelper()
	for _, obj := range objs {
		resource.Apply(obj, scheme.Scheme)
	}
}

func (o *CreateClusterOptions) GenerateObjects() []runtime.Object {
	result := []runtime.Object{}
	cd := hivev1.ClusterDeployment{}
	cd.Name = o.Name
	cd.Namespace = o.Namespace
	result = append(result, cd)

}

const test1 = `
apiVersion: v1
kind: Template
metadata:
  name: cluster-deployment-template

parameters:
- name: CLUSTER_NAME
  displayName: Cluster Name
  description: The name to give to the Cluster created. If using real AWS, then this name should include your username so that resources created in AWS can be identified as yours.
  required: true
- name: SSH_KEY
  displayName: SSH Key
  description: Your public SSH key to reach instances.
  required: true
- name: BASE_DOMAIN
  displayName: Base DNS Domain
  description: Base DNS domain for your cluster. Will be combined with cluster name when creating entries.
  value: new-installer.openshift.com
- name: PULL_SECRET
  displayName: Pull Secret for OpenShift Images
  description: Pull Secret for OpenShift Images
  required: true
- name: AWS_ACCESS_KEY_ID
  required: true
  description: Base64 encoded AWS access key ID that can be used to provision cluster resources.
- name: AWS_SECRET_ACCESS_KEY
  required: true
  description: Base64 encoded AWS secret access key that can be used to provision cluster resources.
- name: HIVE_IMAGE
  displayName: Hive image URL
  description: Hive image URL
  value: ""
- name: HIVE_IMAGE_PULL_POLICY
  displayName: Hive image pull policy
  description: Hive image pull policy
  value: Always
- name: INSTALLER_IMAGE
  displayName: OpenShift Installer image
  description: OpenShift Installer image. Leave empty to use the default installer image from the release image.
  value: ""
- name: INSTALLER_IMAGE_PULL_POLICY
  displayName: OpenShift Installer image pull policy
  description: OpenShift Installer image pull policy
  value: Always
- name: RELEASE_IMAGE
  displayName: OpenShift Release Image
  description: The release image for the version of OpenShift you wish to install. Leave empty to use latest release image.
  value: ""
- name: TRY_INSTALL_ONCE
  displayName: Try Install Only Once
  description: If set, install will only be tried once.
  value: "false"
- name: TRY_UNINSTALL_ONCE
  displayName: Try Uninstall Only Once
  description: If set, uninstall will only be tried once.
  value: "false"
- name: CLUSTER_IMAGE_SET
  displayName: ClusterImageSet to use for the cluster deployment
  description: Specify an alternate ClusterImageSet to reference from the cluster deployment. Default is latest OpenShift 4.0 CI images.
  value: openshift-v4.0-latest

objects:
- apiVersion: v1
  kind: Secret
  metadata:
    name: ${CLUSTER_NAME}-aws-creds
  type: Opaque
  stringData:
    aws_access_key_id: ${AWS_ACCESS_KEY_ID}
    aws_secret_access_key: ${AWS_SECRET_ACCESS_KEY}

- apiVersion: v1
  kind: Secret
  metadata:
    name: ${CLUSTER_NAME}-pull-secret
  type: kubernetes.io/dockerconfigjson
  stringData:
    ".dockerconfigjson": "${PULL_SECRET}"

- apiVersion: v1
  kind: Secret
  metadata:
    name: ${CLUSTER_NAME}-ssh-key
  type: Opaque
  stringData:
    ssh-publickey: "${SSH_KEY}"

- apiVersion: hive.openshift.io/v1alpha1
  kind: ClusterDeployment
  metadata:
    labels:
      controller-tools.k8s.io: "1.0"
    annotations:
      hive.openshift.io/delete-after: "8h"
      hive.openshift.io/try-install-once: "${TRY_INSTALL_ONCE}"
      hive.openshift.io/try-uninstall-once: "${TRY_UNINSTALL_ONCE}"
    name: ${CLUSTER_NAME}
  spec:
    platformSecrets:
      aws:
        credentials:
          name: "${CLUSTER_NAME}-aws-creds"
    images:
      hiveImage: "${HIVE_IMAGE}"
      hiveImagePullPolicy: "${HIVE_IMAGE_PULL_POLICY}"
      installerImage: "${INSTALLER_IMAGE}"
      installerImagePullPolicy: "${INSTALLER_IMAGE_PULL_POLICY}"
      releaseImage: "${RELEASE_IMAGE}"
    imageSet:
      name: "${CLUSTER_IMAGE_SET}"
    sshKey:
      name: "${CLUSTER_NAME}-ssh-key"
    clusterName: ${CLUSTER_NAME}
    baseDomain: ${BASE_DOMAIN}
    networking:
      type: OpenShiftSDN
      serviceCIDR: "172.30.0.0/16"
      machineCIDR: "10.0.0.0/16"
      clusterNetworks:
        - cidr: "10.128.0.0/14"
          hostSubnetLength: 23
    platform:
      aws:
        region: us-east-1
    pullSecret:
      name: "${CLUSTER_NAME}-pull-secret"
    controlPlane:
      name: master
      replicas: 3
      platform:
        aws:
          type: m4.large
          rootVolume:
            iops: 100 # TODO
            size: 22
            type: gp2
    compute:
    - name: worker
      replicas: 3
      platform:
        aws:
          type: m4.large
          rootVolume:
            iops: 100 # TODO
            size: 22
            type: gp2
`
