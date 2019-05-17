package hivecontroller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hive/test/e2e/common"
)

const (
	hiveNamespace             = "hive"
	hiveControllersDeployment = "hive-controllers"
	hiveControllersService    = "hive-controllers"
)

func waitForManager(t *testing.T) bool {
	kubeClient := common.MustGetKubernetesClient()
	err := common.WaitForDeploymentReady(kubeClient, hiveNamespace, hiveControllersDeployment, 10*time.Minute)
	if err != nil {
		t.Errorf("Failed waiting for hive controllers deployment: %v", err)
		return false
	}
	return true
}

func TestHiveControllersDeployment(t *testing.T) {
	if !waitForManager(t) {
		return
	}
	// Ensure that the deployment has 1 available replica
	c := common.MustGetClient()
	deployment := &appsv1.Deployment{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: hiveControllersDeployment, Namespace: hiveNamespace}, deployment)
	if err != nil {
		t.Errorf("Failed to get hive controllers deployment: %v", err)
		return
	}
	if deployment.Status.AvailableReplicas != 1 {
		t.Errorf("Unexpected controller manager available replicas: %d", deployment.Status.AvailableReplicas)
	}
}

func TestHiveControllersMetrics(t *testing.T) {
	if !waitForManager(t) {
		return
	}

	c := common.MustGetClient()
	service := &corev1.Service{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: hiveControllersService, Namespace: hiveNamespace}, service)
	if err != nil {
		t.Errorf("Failed to get hive controllers service: %v", err)
		return
	}
	pods := &corev1.PodList{}
	opts := client.MatchingLabels(service.Spec.Selector).InNamespace(hiveNamespace)
	err := c.List(context.TODO(), opts, pods)
	if err != nil {
		t.Errorf("cannot fetch list of controller pods: %v", err)
		return
	}
	if len(pods.Items) == 0 {
		t.Errorf("no pods were found matching the service selector")
		return
	}

	/*
		service.Spec.Selector
		c.List(
	*/
}
