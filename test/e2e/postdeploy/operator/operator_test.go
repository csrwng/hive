package operator

import (
	"testing"

	"github.com/openshift/hive/test/e2e/common"
)

func TestOperatorRunning(t *testing.T) {
	c := common.MustGetClient()
	err := common.WaitForDeployment(c, hiveNamespace, hiveOperatorDeployment)
	if err != nil {
		t.Errorf("Failed waiting for hive operator deployment")
	}
}

func TestHiveCRDs(t *testing.T) {

}
