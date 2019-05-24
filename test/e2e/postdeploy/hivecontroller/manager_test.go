package hivecontroller

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apimachinery/pkg/labels"
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
	err = c.List(context.TODO(), opts, pods)
	if err != nil {
		t.Errorf("cannot fetch list of controller pods: %v", err)
		return
	}
	if len(pods.Items) == 0 {
		t.Errorf("no pods were found matching the service selector")
		return
	}

	metricsPort := 0
	for _, port := range service.Spec.Ports {
		if port.Name == "metrics" {
			metricsPort = int(port.Port)
		}
	}
	if metricsPort == 0 {
		t.Errorf("cannot find metrics port in hive controllers service")
	}
	localPort := 20000 + rand.Intn(20000)
	portSpec := fmt.Sprintf("%d:%d", localPort, metricsPort)

	forwardOutput := &bytes.Buffer{}

	portForwarder := common.PortForwarder{
		Name:      pods.Items[0].Name,
		Namespace: pods.Items[0].Namespace,
		Stdout:    forwardOutput,
		Stderr:    forwardOutput,
		Client:    common.MustGetKubernetesClient(),
		Config:    common.MustGetConfig(),
	}

	stopChan := make(chan struct{})
	err = portForwarder.ForwardPorts([]string{portSpec}, stopChan)
	if err != nil {
		t.Errorf("cannot start port forwarding")
	}
	defer t.Logf("forward output:\n%s\n", forwardOutput.String())
	defer func() { stopChan <- struct{}{} }()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", localPort))
	if err != nil {
		t.Errorf("unable to fetch metrics endpoint: %v", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("cannot read metrics endpoint response: %v", err)
	}
	if !strings.Contains(string(body), "hive_cluster_deployment_install_job_delay_seconds_bucket") {
		t.Errorf("metrics response does not contain expected metric name")
	}
	t.Logf("metrics response:\n%s\n", string(body))
}
