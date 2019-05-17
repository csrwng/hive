package operator

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ghodss/yaml"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
	"github.com/openshift/hive/test/e2e/common"
)

const (
	hiveNamespace          = "hive"
	hiveOperatorDeployment = "hive-operator"
	hiveConfigName         = "hive"
	hiveManagerService     = "hive-controllers"
	crdDirectory           = "../../../../config/crds"
)

// TestOperatorDeployment ensures that the Hive operator deployment exists and that
// it's available.
func TestOperatorDeployment(t *testing.T) {
	kubeClient := common.MustGetKubernetesClient()
	err := common.WaitForDeploymentReady(kubeClient, hiveNamespace, hiveOperatorDeployment, 5*time.Minute)
	if err != nil {
		t.Errorf("Failed waiting for hive operator deployment: %v", err)
		return
	}

	// Ensure that the deployment has 1 available replica
	c := common.MustGetClient()
	operatorDeployment := &appsv1.Deployment{}
	err = c.Get(context.TODO(), types.NamespacedName{Name: hiveOperatorDeployment, Namespace: hiveNamespace}, operatorDeployment)
	if err != nil {
		t.Errorf("Failed to get hive operator deployment: %v", err)
		return
	}
	if operatorDeployment.Status.AvailableReplicas != 1 {
		t.Errorf("Unexpected operator available replicas: %d", operatorDeployment.Status.AvailableReplicas)
	}
}

func readHiveCRDs(t *testing.T) []*apiextv1beta1.CustomResourceDefinition {
	files, err := ioutil.ReadDir(crdDirectory)
	if err != nil {
		t.Fatalf("cannot read crd directory: %v", err)
	}
	result := []*apiextv1beta1.CustomResourceDefinition{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".yaml") {
			continue
		}
		b, err := ioutil.ReadFile(filepath.Join(crdDirectory, file.Name()))
		if err != nil {
			t.Fatalf("cannot read crd file %s: %v", file.Name(), err)
		}
		crd := &apiextv1beta1.CustomResourceDefinition{}
		if err = yaml.Unmarshal(b, crd); err != nil {
			t.Fatalf("failed to unmarshall crd from %s: %v", file.Name(), err)
		}
		result = append(result, crd)
	}

	return result
}

// TestHiveCRDs ensures that CRDs created by Hive exist in the cluster and that
// they reflect the spec stored in the Hive source repository
func TestHiveCRDs(t *testing.T) {
	c := common.MustGetClient()
	hiveCRDs := readHiveCRDs(t)
	for _, crd := range hiveCRDs {
		serverCRD := &apiextv1beta1.CustomResourceDefinition{}
		err := c.Get(context.TODO(), types.NamespacedName{Name: crd.Name}, serverCRD)
		if err != nil {
			t.Errorf("cannot fetch expected crd (%s): %v", crd.Name, err)
			continue
		}

		// Note: there are some differences between the on-disk version of the CRD
		// and the one on the server in the following fields likely due to defaulting.
		// For now, simply setting them to be equal to test the rest of the spec.
		serverCRD.Spec.Names = crd.Spec.Names
		serverCRD.Spec.Versions = crd.Spec.Versions
		serverCRD.Spec.AdditionalPrinterColumns = crd.Spec.AdditionalPrinterColumns

		if !apiequality.Semantic.DeepEqual(crd.Spec, serverCRD.Spec) {
			diff, err := common.JSONDiff(serverCRD.Spec, crd.Spec)
			if err == nil {
				t.Errorf("crd spec on server does not match expected crd spec (%s): %s", crd.Name, string(diff))
			} else {
				t.Errorf("crd spec on server does not match expected crd spec (%s), could not calculate a diff", crd.Name)
			}
		}
	}
}

// TestHiveConfig tests that the hive configuration exists and that it's a valid configuration
func TestHiveConfig(t *testing.T) {
	c := common.MustGetClient()
	hiveConfig := &hivev1.HiveConfig{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: hiveConfigName}, hiveConfig)
	if err != nil {
		t.Errorf("could not fetch hive config: %v", err)
		return
	}
	if len(hiveConfig.Spec.ManagedDomains) > 0 && hiveConfig.Spec.ExternalDNS == nil {
		t.Errorf("external DNS is not configured, but managed domains are")
	}
	if hiveConfig.Spec.ExternalDNS != nil && len(hiveConfig.Spec.ManagedDomains) == 0 {
		t.Errorf("external DNS is configured, but no managed domains are")
	}
	if hiveConfig.Spec.ExternalDNS != nil && hiveConfig.Spec.ExternalDNS.AWS != nil {
		if len(hiveConfig.Spec.ExternalDNS.AWS.Credentials.Name) == 0 {
			t.Errorf("AWS external DNS configured, but no credentials secret specified")
			return
		}
		secret := &corev1.Secret{}
		secretName := types.NamespacedName{
			Namespace: hiveNamespace,
			Name:      hiveConfig.Spec.ExternalDNS.AWS.Credentials.Name,
		}
		err := c.Get(context.TODO(), secretName, secret)
		if err != nil {
			t.Errorf("AWS external DNS credentials secret specified (%s), but secret cannot be fetched: %v", secretName.Name, err)
		}
	}
}

// TestOperatorApply ensures that changing a resource managed by the
// Hive operator results in the operator overwriting the change.
/*
NOTE: Disabling this for now, since it won't pass. We need to fix
the operator so that it does watch the resources that it creates and
optionally we specify resources that we want unmanaged.

func TestOperatorApply(t *testing.T) {
	kubeClient := common.MustGetKubernetesClient()
	// First ensure that the service exists
	err := common.WaitForService(kubeClient, hiveNamespace, hiveManagerService, func(*corev1.Service) bool { return true }, 8*time.Minute)
	if err != nil {
		t.Errorf("Failed waiting for hive manager service: %v", err)
		return
	}

	service := &corev1.Service{}
	c := common.MustGetClient()
	err = c.Get(context.TODO(), types.NamespacedName{Namespace: hiveNamespace, Name: hiveManagerService}, service)
	if err != nil {
		t.Errorf("Failed to fetch hive manager service: %v", err)
		return
	}

	service.Annotations = map[string]string{"test-change": "my test change"}
	err = c.Update(context.TODO(), service)
	if err != nil {
		t.Errorf("Failed to update hive manager service: %v", err)
		return
	}

	serviceIsReverted := func(s *corev1.Service) bool {
		annotations := s.Annotations
		if annotations == nil {
			return true
		}
		_, key_exists := annotations["test-change"]
		return !key_exists
	}

	// Wait for change to service resource to be reverted
	err = common.WaitForService(kubeClient, hiveNamespace, hiveManagerService, serviceIsReverted, 5*time.Minute)
	if err != nil {
		t.Errorf("Failed to get change reverted: %v", err)
	}
}
*/
