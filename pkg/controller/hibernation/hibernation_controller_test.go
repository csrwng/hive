package hibernation

import (
	// "context"
	// "fmt"
	// "reflect"
	"testing"

	// "github.com/golang/mock/gomock"
	log "github.com/sirupsen/logrus"
	// "github.com/stretchr/testify/assert"
	// "github.com/stretchr/testify/require"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	// "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	// "github.com/openshift/hive/pkg/constants"
	// controllerutils "github.com/openshift/hive/pkg/controller/utils"
	// "github.com/openshift/hive/pkg/remoteclient"
	// remoteclientmock "github.com/openshift/hive/pkg/remoteclient/mock"
	testcd "github.com/openshift/hive/pkg/test/clusterdeployment"
	// testcm "github.com/openshift/hive/pkg/test/configmap"
	// testdnszone "github.com/openshift/hive/pkg/test/dnszone"
	testgeneric "github.com/openshift/hive/pkg/test/generic"
	// testjob "github.com/openshift/hive/pkg/test/job"
	// testmp "github.com/openshift/hive/pkg/test/machinepool"
	// testnamespace "github.com/openshift/hive/pkg/test/namespace"
	// testsecret "github.com/openshift/hive/pkg/test/secret"
	// testsip "github.com/openshift/hive/pkg/test/syncidentityprovider"
	// testss "github.com/openshift/hive/pkg/test/syncset"
)

const (
	namespace = "test-namespace"
	cdName    = "test-cluster-deployment"
)

func TestReconcile(t *testing.T) {
	logger := log.New()
	logger.SetLevel(log.DebugLevel)

	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	batchv1.AddToScheme(scheme)
	hivev1.AddToScheme(scheme)

	cdBuilder := testcd.FullBuilder(namespace, cdName, scheme).GenericOptions(
		testgeneric.WithFinalizer(hivev1.FinalizerDeprovision),
	).Options(
		func(cd *hivev1.ClusterDeployment) {
			cd.Spec.Installed = true
		},
	)

	notInstalled := func(cd *hivev1.ClusterDeployment) {
		cd.Spec.Installed = false
	}
	shouldHibernate := func(cd *hivev1.ClusterDeployment) {
		cd.Spec.PowerState = hivev1.HibernatingClusterPowerState
	}
	hibernatingCondition := func(cd *hivev1.ClusterDeployment) *hivev1.ClusterDeploymentCondition {
		for i := range cd.Status.Conditions {
			if cd.Status.Conditions[i].Type == hivev1.ClusterHibernatingCondition {
				return &cd.Status.Conditions[i]
			}
		}
		return nil
	}

	tests := []struct {
		name     string
		cd       *hivev1.ClusterDeployment
		validate func(t *testing.T, cd *hivev1.ClusterDeployment)
	}{
		{
			name: "cluster deleted",
			cd:   cdBuilder.GenericOptions(testgeneric.Deleted()).Options(shouldHibernate).Build(),
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				if hibernatingCondition(cd) != nil {
					t.Errorf("not expecting hibernating condition")
				}
			},
		},
		{
			name: "cluster not installed",
			cd:   cdBuilder.Options(notInstalled, shouldHibernate).Build(),
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				if hibernatingCondition(cd) != nil {
					t.Errorf("not expecting hibernating condition")
				}
			},
		},
		{
			name: "should hibernate",
			cd:   cdBuilder.Options(shouldHibernate).Build(),
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {

			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewFakeClientWithScheme(scheme, test.cd)
			reconciler := hibernationReconciler{
				Client:              client,
				scheme:              scheme,
				logger:              log.WithField("controller", "hibernation"),
				remoteClientBuilder: nil, // TODO
			}
			reconciler.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: cdName},
			})
		})
	}

}
