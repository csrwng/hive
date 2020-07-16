package hibernation

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awsclient "github.com/openshift/hive/pkg/awsclient"
	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
)

func init() {
	RegisterActuator(&awsActuator{})
}

type awsActuator struct {
}

// CanHandle returns true if the actuator can handle a particular ClusterDeployment
func (a *awsActuator) CanHandle(cd *hivev1.ClusterDeployment) bool {
	return cd.Spec.Platform.AWS != nil
}

// StopMachines will stop machines belonging to the given ClusterDeployment
func (a *awsActuator) StopMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c client.Client) error {
	logger = logger.WithField("cloud", "aws")
	logger.Info("stopping machines")
	awsClient, err := getAWSClient(logger, cd, c)
	if err != nil {
		return err
	}
	instanceIDs, err := getClusterInstanceIDs(logger, cd, awsClient, []string{"pending", "running"})
	if err != nil {
		return err
	}
	if len(instanceIDs) == 0 {
		logger.Warning("no instances were found to stop")
		return nil
	}
	_, err = awsClient.StopInstances(&ec2.StopInstancesInput{
		InstanceIds: instanceIDs,
	})
	if err != nil {
		logger.WithError(err).Error("failed to stop instances")
	}
	return err
}

// StartMachines will select machines belonging to the given ClusterDeployment
func (a *awsActuator) StartMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c client.Client) error {
	logger = logger.WithField("cloud", "aws")
	logger.Infof("starting machines")
	awsClient, err := getAWSClient(logger, cd, c)
	if err != nil {
		return err
	}
	instanceIDs, err := getClusterInstanceIDs(logger, cd, awsClient, []string{"stopping", "stopped", "shutting-down"})
	if err != nil {
		return err
	}
	if len(instanceIDs) == 0 {
		logger.Warning("no instances were found to start")
		return nil
	}
	_, err = awsClient.StartInstances(&ec2.StartInstancesInput{
		InstanceIds: instanceIDs,
	})
	if err != nil {
		logger.WithError(err).Error("failed to start instances")
	}
	return err
}

// MachinesRunning will return true if the machines associated with the given
// ClusterDeployment are in a running state.
func (a *awsActuator) MachinesRunning(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c client.Client) (bool, error) {
	logger = logger.WithField("cloud", "aws")
	logger.Infof("checking whether machines are running")
	awsClient, err := getAWSClient(logger, cd, c)
	if err != nil {
		return false, err
	}
	instanceIDs, err := getClusterInstanceIDs(logger, cd, awsClient, []string{"stopping", "stopped", "pending", "shutting-down"})
	if err != nil {
		return false, err
	}
	return len(instanceIDs) == 0, nil
}

// MachinesStopped will return true if the machines associated with the given
// ClusterDeployment are in a stopped state.
func (a *awsActuator) MachinesStopped(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c client.Client) (bool, error) {
	logger = logger.WithField("cloud", "aws")
	logger.Infof("checking whether machines are stopped")
	awsClient, err := getAWSClient(logger, cd, c)
	if err != nil {
		return false, err
	}
	instanceIDs, err := getClusterInstanceIDs(logger, cd, awsClient, []string{"running", "pending", "stopping", "shutting-down"})
	if err != nil {
		return false, err
	}
	return len(instanceIDs) == 0, nil
}

func getAWSClient(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c client.Client) (awsclient.Client, error) {
	awsClient, err := awsclient.NewClient(c, cd.Spec.Platform.AWS.CredentialsSecretRef.Name, cd.Namespace, cd.Spec.Platform.AWS.Region)
	if err != nil {
		logger.WithError(err).Error("failed to get AWS client")
	}
	return awsClient, err
}

func getClusterInstanceIDs(logger log.FieldLogger, cd *hivev1.ClusterDeployment, c awsclient.Client, states []string) ([]*string, error) {
	validStates := sets.NewString(states...)
	infraID := cd.Spec.ClusterMetadata.InfraID
	logger = logger.WithField("infraID", infraID)
	logger.Debug("listing cluster instances")
	out, err := c.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String(fmt.Sprintf("tag:kubernetes.io/cluster/%s", infraID)),
				Values: []*string{aws.String("owned")},
			},
		},
	})
	if err != nil {
		logger.WithError(err).Error("failed to list instances")
		return nil, err
	}
	result := []*string{}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			if validStates.Has(aws.StringValue(i.State.Name)) {
				result = append(result, i.InstanceId)
			}
		}
	}
	logger.WithField("count", len(result)).WithField("states", states).Debug("result of listing instances")
	return result, nil
}
