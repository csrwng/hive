package hibernation

import (
	"context"
	"fmt"
	// "reflect"
	"testing"

	"github.com/golang/mock/gomock"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	machineapi "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	// "github.com/openshift/hive/pkg/constants"
	// controllerutils "github.com/openshift/hive/pkg/controller/utils"
	"github.com/openshift/hive/pkg/remoteclient"
	remoteclientmock "github.com/openshift/hive/pkg/remoteclient/mock"
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
	"github.com/openshift/hive/pkg/controller/hibernation/mock"
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
	machineapi.AddToScheme(scheme)

	cdBuilder := testcd.FullBuilder(namespace, cdName, scheme).GenericOptions(
		testgeneric.WithFinalizer(hivev1.FinalizerDeprovision),
	).Options(
		func(cd *hivev1.ClusterDeployment) {
			cd.Spec.Installed = true
			cd.Status.ClusterVersionStatus = &configv1.ClusterVersionStatus{
				Desired: configv1.Update{
					Version: "4.4.9",
				},
			}
			cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
				Type:   hivev1.UnreachableCondition,
				Status: corev1.ConditionFalse,
				Reason: "Reachable",
			})
		},
	)
	o := clusterDeploymentOptions{}

	tests := []struct {
		name          string
		cd            *hivev1.ClusterDeployment
		setupActuator func(actuator *mock.MockHibernationActuator)
		setupRemote   func(builder *remoteclientmock.MockBuilder)
		validate      func(t *testing.T, cd *hivev1.ClusterDeployment)
	}{
		{
			name: "cluster deleted",
			cd:   cdBuilder.GenericOptions(testgeneric.Deleted()).Options(o.shouldHibernate).Build(),
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				if getHibernatingCondition(cd) != nil {
					t.Errorf("not expecting hibernating condition")
				}
			},
		},
		{
			name: "cluster not installed",
			cd:   cdBuilder.Options(o.notInstalled, o.shouldHibernate).Build(),
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				require.Nil(t, getHibernatingCondition(cd))
			},
		},
		{
			name: "start hibernating",
			cd:   cdBuilder.Options(o.shouldHibernate).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().StopMachines(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.StoppingHibernationReason, cond.Reason)
			},
		},
		{
			name: "stopping, machines have stopped",
			cd:   cdBuilder.Options(o.shouldHibernate, o.stopping).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesStopped(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(true, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.HibernatingHibernationReason, cond.Reason)
			},
		},
		{
			name: "stopping, machines have not stopped",
			cd:   cdBuilder.Options(o.shouldHibernate, o.stopping).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesStopped(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(false, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.StoppingHibernationReason, cond.Reason)
			},
		},
		{
			name: "start resuming",
			cd:   cdBuilder.Options(o.hibernating).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().StartMachines(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.ResumingHibernationReason, cond.Reason)
			},
		},
		{
			name: "starting, machines have not started",
			cd:   cdBuilder.Options(o.resuming).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesRunning(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(false, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.ResumingHibernationReason, cond.Reason)
			},
		},
		{
			name: "starting, machines running, nodes ready",
			cd:   cdBuilder.Options(o.resuming).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesRunning(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(true, nil)
			},
			setupRemote: func(builder *remoteclientmock.MockBuilder) {
				c := fake.NewFakeClientWithScheme(scheme, readyNodes()...)
				builder.EXPECT().Build().Times(1).Return(c, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionFalse, cond.Status)
				assert.Equal(t, hivev1.RunningHibernationReason, cond.Reason)
			},
		},
		{
			name: "starting, machines running, unready node",
			cd:   cdBuilder.Options(o.resuming).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesRunning(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(true, nil)
			},
			setupRemote: func(builder *remoteclientmock.MockBuilder) {
				fakeClient := fake.NewFakeClientWithScheme(scheme, unreadyNode()...)
				fakeKubeClient := fakekubeclient.NewSimpleClientset()
				builder.EXPECT().Build().Times(2).Return(fakeClient, nil)
				builder.EXPECT().BuildKubeClient().Times(1).Return(fakeKubeClient, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.ResumingHibernationReason, cond.Reason)
			},
		},
		{
			name: "starting, machines running, unready node, csrs to approve",
			cd:   cdBuilder.Options(o.resuming).Build(),
			setupActuator: func(actuator *mock.MockHibernationActuator) {
				actuator.EXPECT().MachinesRunning(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(true, nil)
			},
			setupRemote: func(builder *remoteclientmock.MockBuilder) {
				fakeClient := fake.NewFakeClientWithScheme(scheme, unreadyNode()...)
				fakeKubeClient := fakekubeclient.NewSimpleClientset()
				builder.EXPECT().Build().Times(2).Return(fakeClient, nil)
				builder.EXPECT().BuildKubeClient().Times(1).Return(fakeKubeClient, nil)
			},
			validate: func(t *testing.T, cd *hivev1.ClusterDeployment) {
				cond := getHibernatingCondition(cd)
				require.NotNil(t, cond)
				assert.Equal(t, corev1.ConditionTrue, cond.Status)
				assert.Equal(t, hivev1.ResumingHibernationReason, cond.Reason)
			},
		},
	}

	for _, test := range tests[len(tests)-1:] {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockActuator := mock.NewMockHibernationActuator(ctrl)
			mockActuator.EXPECT().CanHandle(gomock.Any()).AnyTimes().Return(true)
			if test.setupActuator != nil {
				test.setupActuator(mockActuator)
			}
			mockBuilder := remoteclientmock.NewMockBuilder(ctrl)
			if test.setupRemote != nil {
				test.setupRemote(mockBuilder)
			}
			actuators = []HibernationActuator{mockActuator}
			c := fake.NewFakeClientWithScheme(scheme, test.cd)

			reconciler := hibernationReconciler{
				Client: c,
				scheme: scheme,
				logger: log.WithField("controller", "hibernation"),
				remoteClientBuilder: func(cd *hivev1.ClusterDeployment) remoteclient.Builder {
					return mockBuilder
				},
			}
			_, err := reconciler.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: cdName},
			})
			assert.Nil(t, err)
			if test.validate != nil {
				cd := &hivev1.ClusterDeployment{}
				err := c.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: cdName}, cd)
				require.Nil(t, err)
				test.validate(t, cd)
			}
			ctrl.Finish()
		})
	}

}

type clusterDeploymentOptions struct{}

func (*clusterDeploymentOptions) notInstalled(cd *hivev1.ClusterDeployment) {
	cd.Spec.Installed = false
}
func (*clusterDeploymentOptions) shouldHibernate(cd *hivev1.ClusterDeployment) {
	cd.Spec.PowerState = hivev1.HibernatingClusterPowerState
}
func (*clusterDeploymentOptions) stopping(cd *hivev1.ClusterDeployment) {
	cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
		Type:   hivev1.ClusterHibernatingCondition,
		Reason: hivev1.StoppingHibernationReason,
		Status: corev1.ConditionTrue,
	})
}
func (*clusterDeploymentOptions) hibernating(cd *hivev1.ClusterDeployment) {
	cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
		Type:   hivev1.ClusterHibernatingCondition,
		Reason: hivev1.HibernatingHibernationReason,
		Status: corev1.ConditionTrue,
	})
}
func (*clusterDeploymentOptions) resuming(cd *hivev1.ClusterDeployment) {
	cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
		Type:   hivev1.ClusterHibernatingCondition,
		Reason: hivev1.ResumingHibernationReason,
		Status: corev1.ConditionTrue,
	})
}

func getHibernatingCondition(cd *hivev1.ClusterDeployment) *hivev1.ClusterDeploymentCondition {
	for i := range cd.Status.Conditions {
		if cd.Status.Conditions[i].Type == hivev1.ClusterHibernatingCondition {
			return &cd.Status.Conditions[i]
		}
	}
	return nil
}

func readyNodes() []runtime.Object {
	nodes := make([]runtime.Object, 5)
	for i := 0; i < len(nodes); i++ {
		node := &corev1.Node{}
		node.Name = fmt.Sprintf("node-%d", i)
		node.Status.Conditions = []corev1.NodeCondition{
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionTrue,
			},
		}
		nodes[i] = node
	}
	return nodes
}

func unreadyNode() []runtime.Object {
	node := &corev1.Node{}
	node.Name = "unready"
	node.Status.Conditions = []corev1.NodeCondition{
		{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionFalse,
		},
	}
	return append(readyNodes(), node)
}
