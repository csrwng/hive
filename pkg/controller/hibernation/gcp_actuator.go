package hibernation

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	compute "google.golang.org/api/compute/v1"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
	"github.com/openshift/hive/pkg/gcpclient"
)

const (
	instanceFields = "items/*/instances(name,zone,status),nextPageToken"
)

var (
	gcpRunningStatuses           = sets.NewString("RUNNING")
	gcpStoppedStatuses           = sets.NewString("STOPPED", "TERMINATED")
	gcpPendingStatuses           = sets.NewString("PROVISIONING", "STAGING")
	gcpStoppingStatuses          = sets.NewString("STOPPING")
	gcpRunningOrPendingStatuses  = gcpRunningStatuses.Union(gcpPendingStatuses)
	gcpStoppedOrStoppingStatuses = gcpStoppedStatuses.Union(gcpStoppingStatuses)
	gcpNotRunningStatuses        = gcpStoppedOrStoppingStatuses.Union(gcpPendingStatuses)
	gcpNotStoppedStatuses        = gcpRunningOrPendingStatuses.Union(gcpStoppingStatuses)
)

func init() {
	RegisterActuator(&gcpActuator{getGCPClientFn: getGCPClient})
}

type gcpActuator struct {
	getGCPClientFn func(*hivev1.ClusterDeployment, client.Client, log.FieldLogger) (gcpclient.Client, error)
}

// CanHandle returns true if the actuator can handle a particular ClusterDeployment
func (a *gcpActuator) CanHandle(cd *hivev1.ClusterDeployment) bool {
	return cd.Spec.Platform.GCP != nil
}

// StopMachines will start machines belonging to the given ClusterDeployment
func (a *gcpActuator) StopMachines(cd *hivev1.ClusterDeployment, hiveClient client.Client, logger log.FieldLogger) error {
	logger = logger.WithField("cloud", "GCP")
	gcpClient, err := a.getGCPClientFn(cd, hiveClient, logger)
	if err != nil {
		return err
	}
	instances, err := gcpClient.ListComputeInstances(gcpclient.ListComputeInstancesOptions{
		Filter: instanceFilter(cd),
		Fields: instanceFields,
	})
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch instances")
		return errors.Wrap(err, "failed to fetch instances")
	}
	instances = filterByStatus(instances, gcpRunningOrPendingStatuses)
	errs := []error{}
	for _, instance := range instances {
		logger.WithField("instance", instance.Name).Info("Stopping instance")
		err = gcpClient.StopInstance(instance)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

// StartMachines will select machines belonging to the given ClusterDeployment
func (a *gcpActuator) StartMachines(cd *hivev1.ClusterDeployment, hiveClient client.Client, logger log.FieldLogger) error {
	logger = logger.WithField("cloud", "GCP")
	gcpClient, err := a.getGCPClientFn(cd, hiveClient, logger)
	if err != nil {
		return err
	}
	instances, err := gcpClient.ListComputeInstances(gcpclient.ListComputeInstancesOptions{
		Filter: instanceFilter(cd),
		Fields: instanceFields,
	})
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch instances")
		return errors.Wrap(err, "failed to fetch instances")
	}
	instances = filterByStatus(instances, gcpStoppedOrStoppingStatuses)
	errs := []error{}
	for _, instance := range instances {
		logger.WithField("instance", instance.Name).Info("Starting instance")
		err = gcpClient.StartInstance(instance)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

// MachinesRunning will return true if the machines associated with the given
// ClusterDeployment are in a running state.
func (a *gcpActuator) MachinesRunning(cd *hivev1.ClusterDeployment, hiveClient client.Client, logger log.FieldLogger) (bool, error) {
	logger = logger.WithField("cloud", "GCP")
	gcpClient, err := a.getGCPClientFn(cd, hiveClient, logger)
	if err != nil {
		return false, err
	}
	instances, err := gcpClient.ListComputeInstances(gcpclient.ListComputeInstancesOptions{
		Filter: instanceFilter(cd),
		Fields: instanceFields,
	})
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch instances")
		return false, errors.Wrap(err, "failed to fetch instances")
	}
	instances = filterByStatus(instances, gcpNotRunningStatuses)
	return len(instances) == 0, nil
}

// MachinesStopped will return true if the machines associated with the given
// ClusterDeployment are in a stopped state.
func (a *gcpActuator) MachinesStopped(cd *hivev1.ClusterDeployment, hiveClient client.Client, logger log.FieldLogger) (bool, error) {
	logger = logger.WithField("cloud", "GCP")
	gcpClient, err := a.getGCPClientFn(cd, hiveClient, logger)
	if err != nil {
		return false, err
	}
	instances, err := gcpClient.ListComputeInstances(gcpclient.ListComputeInstancesOptions{
		Filter: instanceFilter(cd),
		Fields: instanceFields,
	})
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch instances")
		return false, errors.Wrap(err, "failed to fetch instances")
	}
	instances = filterByStatus(instances, gcpNotStoppedStatuses)
	return len(instances) == 0, nil
}

func getGCPClient(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) (gcpclient.Client, error) {
	if cd.Spec.Platform.GCP == nil {
		return nil, errors.New("GCP platform is not set in ClusterDeployment")
	}
	secret := &corev1.Secret{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: cd.Spec.Platform.GCP.CredentialsSecretRef.Name, Namespace: cd.Namespace}, secret)
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch GCP credentials secret")
		return nil, errors.Wrap(err, "failed to fetch GCP credentials secret")
	}
	return gcpclient.NewClientFromSecret(secret)
}

func instanceFilter(cd *hivev1.ClusterDeployment) string {
	return fmt.Sprintf("name eq \"%s-.*\"", cd.Spec.ClusterMetadata.InfraID)
}

func filterByStatus(instances []*compute.Instance, statuses sets.String) []*compute.Instance {
	result := []*compute.Instance{}
	for _, instance := range instances {
		if statuses.Has(instance.Status) {
			result = append(result, instance)
		}
	}
	return result
}
