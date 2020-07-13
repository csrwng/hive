package hibernation

import (
	log "github.com/sirupsen/logrus"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
)

// HibernationActuator is the interface that the hibernation controller uses to
// interact with cloud providers.
type HibernationActuator interface {
	// CanHandle returns true if the actuator can handle a particular ClusterDeployment
	CanHandle(cd *hivev1.ClusterDeployment) bool
	// StopMachines will start machines belonging to the given ClusterDeployment
	StopMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment, hiveClient client.Client) error
	// StartMachines will select machines belonging to the given ClusterDeployment
	StartMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment, hiveClient client.Client) error
	// MachinesRunning will return true if the machines associated with the given
	// ClusterDeployment are in a running state.
	MachinesRunning(logger log.FieldLogger, cd *hivev1.ClusterDeployment, hiveClient client.Client) (bool, error)
	// MachinesStopped will return true if the machines associated with the given
	MachinesStopped(logger log.FieldLogger, cd *hivev1.ClusterDeployment, hiveClient client.Client) (bool, error)
}
