package clusterstate

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hive/pkg/apis"
	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
)

func TestClusterStateReconcile(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)
	configv1.Install(scheme.Scheme)

	tests := []struct {
		name      string
		existing  []runtime.Object
		expectErr bool
		validate  func(client.Client, *testing.T)
	}{
		{
			name: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

		})
	}
}
