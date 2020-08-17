package hibernation

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2018-10-01/compute"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	"github.com/openshift/hive/pkg/azureclient"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
)

const (
	azureConcurrentVMInstanceCalls = 5
	azurePowerStatePrefix          = "PowerState/"
	azureUnknownPowerState         = "unknown"
)

var (
	azureRunningStates           = sets.NewString("running")
	azureStoppedStates           = sets.NewString("stopped", "deallocated")
	azurePendingStates           = sets.NewString("starting")
	azureStoppingStates          = sets.NewString("stopping", "deallocating")
	azureRunningOrPendingStates  = azureRunningStates.Union(azurePendingStates)
	azureStoppedOrStoppingStates = azureStoppedStates.Union(azureStoppingStates)
	azureNotRunningStates        = azureStoppedOrStoppingStates.Union(azurePendingStates)
	azureNotStoppedStates        = azureRunningOrPendingStates.Union(azureStoppingStates)
)

func init() {
	RegisterActuator(&azureActuator{azureClientFn: getAzureClient})
}

type azureActuator struct {
	// azureClientFn is the function to build an Azure client, here for testing
	azureClientFn func(*hivev1.ClusterDeployment, client.Client, log.FieldLogger) (azureclient.Client, error)
}

// CanHandle returns true if the actuator can handle a particular ClusterDeployment
func (a *azureActuator) CanHandle(cd *hivev1.ClusterDeployment) bool {
	return cd.Spec.Platform.Azure != nil
}

// StopMachines will stop machines belonging to the given ClusterDeployment
func (a *azureActuator) StopMachines(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) error {
	logger = logger.WithField("cloud", "azure")
	azureClient, err := a.azureClientFn(cd, c, logger)
	if err != nil {
		return err
	}
	machines, err := listAzureMachines(cd, azureClient, azureRunningOrPendingStates, logger)
	if err != nil {
		return err
	}
	if len(machines) == 0 {
		logger.Warning("No machines were found to stop")
		return nil
	}
	errs := []error{}
	for _, machineName := range azureMachineNames(machines) {
		logger.WithField("machine", machineName).Info("Stopping cluster machine")
		_, err = azureClient.DeallocateVirtualMachine(context.TODO(), clusterDeploymentResourceGroup(cd), machineName)
		if err != nil {
			errs = append(errs, err)
			logger.WithError(err).WithField("machine", machineName).Error("Failed to stop machine")
		}
	}
	return utilerrors.NewAggregate(errs)
}

// StartMachines will select machines belonging to the given ClusterDeployment
func (a *azureActuator) StartMachines(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) error {
	logger = logger.WithField("cloud", "azure")
	azureClient, err := a.azureClientFn(cd, c, logger)
	if err != nil {
		return err
	}
	machines, err := listAzureMachines(cd, azureClient, azureStoppedOrStoppingStates, logger)
	if err != nil {
		return err
	}
	if len(machines) == 0 {
		logger.Warning("No machines were found to start")
		return nil
	}
	errs := []error{}
	for _, machineName := range azureMachineNames(machines) {
		logger.WithField("machine", machineName).Info("Starting cluster machine")
		_, err = azureClient.StartVirtualMachine(context.TODO(), clusterDeploymentResourceGroup(cd), machineName)
		if err != nil {
			errs = append(errs, err)
			logger.WithError(err).WithField("machine", machineName).Error("Failed to start machine")
		}
	}
	return utilerrors.NewAggregate(errs)
}

// MachinesRunning will return true if the machines associated with the given
// ClusterDeployment are in a running state.
func (a *azureActuator) MachinesRunning(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) (bool, error) {
	logger = logger.WithField("cloud", "azure")
	logger.Infof("checking whether machines are running")
	azureClient, err := a.azureClientFn(cd, c, logger)
	if err != nil {
		return false, err
	}
	machines, err := listAzureMachines(cd, azureClient, azureNotRunningStates, logger)
	if err != nil {
		return false, err
	}
	return len(machines) == 0, nil
}

// MachinesStopped will return true if the machines associated with the given
// ClusterDeployment are in a stopped state.
func (a *azureActuator) MachinesStopped(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) (bool, error) {
	logger = logger.WithField("cloud", "azure")
	logger.Infof("checking whether machines are stopped")
	azureClient, err := a.azureClientFn(cd, c, logger)
	if err != nil {
		return false, err
	}
	machines, err := listAzureMachines(cd, azureClient, azureNotStoppedStates, logger)
	if err != nil {
		return false, err
	}
	return len(machines) == 0, nil
}

type azureMachineLister struct {
	client        azureclient.Client
	resourceGroup string
	states        sets.String
	done          chan struct{}
	err           chan error
	logger        log.FieldLogger
}

func listAzureMachines(cd *hivev1.ClusterDeployment, azureClient azureclient.Client, states sets.String, logger log.FieldLogger) ([]compute.VirtualMachine, error) {
	rg := clusterDeploymentResourceGroup(cd)
	lister := &azureMachineLister{
		client:        azureClient,
		resourceGroup: rg,
		states:        states,
		done:          make(chan struct{}),
		err:           make(chan error, 1),
		logger:        logger.WithField("resourceGroup", rg),
	}
	return lister.listByState()
}

func (l *azureMachineLister) listByState() ([]compute.VirtualMachine, error) {
	defer close(l.done)
	allVMs := l.listAll()
	filtered := make([]<-chan compute.VirtualMachine, azureConcurrentVMInstanceCalls)
	for i := 0; i < azureConcurrentVMInstanceCalls; i++ {
		filtered[i] = l.filterByState(allVMs)
	}
	merged := l.merge(filtered)
	filteredVMs := l.collect(merged)
	select {
	case err := <-l.err:
		return nil, err
	case result := <-filteredVMs:
		return result, nil
	}
}

func (l *azureMachineLister) listAll() <-chan compute.VirtualMachine {
	vmCh := make(chan compute.VirtualMachine)
	go func() {
		defer close(vmCh)
		l.logger.Debug("Listing all virtual machines in resource group")
		page, err := l.client.ListVirtualMachines(context.TODO(), l.resourceGroup)
		if err != nil {
			l.logger.WithError(err).Error("Failed to list virtual machines")
			l.err <- errors.Wrap(err, "failed to list virtual machines")
			return
		}
		iterator := compute.NewVirtualMachineListResultIterator(page)
		for iterator.NotDone() {
			select {
			case vmCh <- iterator.Value():
			case <-l.done:
				return
			}
			if err = iterator.NextWithContext(context.TODO()); err != nil {
				l.logger.WithError(err).Error("Failed to list next page of virtual machines")
				l.err <- errors.Wrap(err, "failed to list next page of virtual machines")
				return
			}
		}
	}()
	return vmCh
}

func (l *azureMachineLister) filterByState(allVMs <-chan compute.VirtualMachine) <-chan compute.VirtualMachine {
	filtered := make(chan compute.VirtualMachine)
	go func() {
		defer close(filtered)
		for vm := range allVMs {
			if vm.Name == nil {
				l.logger.Warning("Got virtual machine with nil name")
				continue
			}
			instanceView, err := l.client.VirtualMachineInstanceView(context.TODO(), l.resourceGroup, *vm.Name)
			if err != nil {
				l.err <- err
				return
			}
			state := azureMachinePowerState(instanceView)
			if l.states.Has(state) {
				select {
				case filtered <- vm:
				case <-l.done:
					return
				}
			}
		}
	}()
	return filtered
}

func (l *azureMachineLister) collect(merged <-chan compute.VirtualMachine) <-chan []compute.VirtualMachine {
	result := make(chan []compute.VirtualMachine)
	go func() {
		vms := []compute.VirtualMachine{}
		for vm := range merged {
			vms = append(vms, vm)
		}
		result <- vms
	}()
	return result
}

func (l *azureMachineLister) merge(sources []<-chan compute.VirtualMachine) <-chan compute.VirtualMachine {
	wg := sync.WaitGroup{}
	out := make(chan compute.VirtualMachine)
	wg.Add(len(sources))
	for _, s := range sources {
		sourceChan := s
		go func() {
			defer wg.Done()
			for vm := range sourceChan {
				select {
				case out <- vm:
				case <-l.done:
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func azureMachinePowerState(instanceView compute.VirtualMachineInstanceView) string {
	if instanceView.Statuses == nil {
		return azureUnknownPowerState
	}
	for _, s := range *instanceView.Statuses {
		if s.Code != nil && strings.HasPrefix(*s.Code, azurePowerStatePrefix) {
			return strings.TrimPrefix(*s.Code, azurePowerStatePrefix)
		}
	}
	return azureUnknownPowerState
}

func clusterDeploymentResourceGroup(cd *hivev1.ClusterDeployment) string {
	if cd.Spec.ClusterMetadata == nil {
		return ""
	}
	return fmt.Sprintf("%s-rg", cd.Spec.ClusterMetadata.InfraID)
}

func azureMachineNames(machines []compute.VirtualMachine) []string {
	result := []string{}
	for _, m := range machines {
		if m.Name == nil {
			continue
		}
		result = append(result, *m.Name)
	}
	return result
}

func getAzureClient(cd *hivev1.ClusterDeployment, c client.Client, logger log.FieldLogger) (azureclient.Client, error) {
	if cd.Spec.Platform.Azure == nil {
		return nil, errors.New("Azure platform is not set in ClusterDeployment")
	}
	secret := &corev1.Secret{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: cd.Spec.Platform.Azure.CredentialsSecretRef.Name, Namespace: cd.Namespace}, secret)
	if err != nil {
		logger.WithError(err).Log(controllerutils.LogLevel(err), "Failed to fetch Azure credentials secret")
		return nil, errors.Wrap(err, "failed to fetch Azure credentials secret")
	}
	azureClient, err := azureclient.NewClientFromSecret(secret)
	if err != nil {
		logger.WithError(err).Error("failed to get Azure client")
	}
	return azureClient, err
}
