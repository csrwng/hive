package operator

import (
	"testing"
	// "time"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"

	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"

	"github.com/openshift/hive/test/e2e/common"
)

const (
	hiveNamespace          = "hive"
	hiveOperatorDeployment = "hive-operator"
	crdDirectory           = "../../../../config/crds"
)

/*
func TestOperatorRunning(t *testing.T) {
	c := common.MustGetKubernetesClient()
	err := common.WaitForDeploymentReady(c, hiveNamespace, hiveOperatorDeployment, 5*time.Minute)
	if err != nil {
		t.Errorf("Failed waiting for hive operator deployment")
	}
}
*/

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
		if !apiequality.Semantic.DeepEqual(crd.Spec, serverCRD.Spec) {
			t.Errorf("crd spec on server does not match expected crd spec (%s)", crd.Name)
		}
	}
}

func TestHiveConfig(t *testing.T) {

}
