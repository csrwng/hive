package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clientcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hive/contrib/pkg/createcluster"
	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
)

const (
	clusterNameEnvVar        = "CLUSTER_NAME"
	clusterNamespaceEnvVar   = "CLUSTER_NAMESPACE"
	sshPublicKeyFileEnvVar   = "SSH_PUBLIC_KEY_FILE"
	pullSecretFileEnvVar     = "PULL_SECRET_FILE"
	releaseImageEnvVar       = "RELEASE_IMAGE"
	hiveImageEnvVar          = "HIVE_IMAGE"
	baseDomainEnvVar         = "BASE_DOMAIN"
	awsSecretAccessKeyEnvVar = "AWS_SECRET_ACCESS_KEY"
	awsSecretKeyIDEnvVar     = "AWS_ACCESS_KEY_ID"
)

var (
	testTimeout = 30 * time.Minute
)

type createClusterOpt struct {
	Name             string
	Namespace        string
	SSHPublicKeyFile string
	PullSecretFile   string
	ReleaseImage     string
	HiveImage        string
	BaseDomain       string

	logger            log.FieldLogger
	installLog        string
	installJob        string
	startedInstall    bool
	completedInstall  bool
	startedImageSet   bool
	completedImageSet bool
	followingLog      bool
}

func CreateCluster() *cobra.Command {
	opt := &createClusterOpt{}
	cmd := &cobra.Command{
		Use:   "create-cluster",
		Short: "Creates a cluster deployment for e2e",
		Run: func(cmd *cobra.Command, args []string) {
			opt.Complete()

			if err := opt.Validate(); err != nil {
				log.Fatalf("error: %v", err)
			}

			if err := opt.Run(); err != nil {
				log.Fatalf("error: %v", err)
			}
		},
	}
	return cmd
}

func (o *createClusterOpt) Complete() {
	o.Name = os.Getenv(clusterNameEnvVar)
	o.Namespace = os.Getenv(clusterNamespaceEnvVar)
	o.SSHPublicKeyFile = os.Getenv(sshPublicKeyFileEnvVar)
	o.PullSecretFile = os.Getenv(pullSecretFileEnvVar)
	o.ReleaseImage = os.Getenv(releaseImageEnvVar)
	o.HiveImage = os.Getenv(hiveImageEnvVar)
	o.BaseDomain = os.Getenv(baseDomainEnvVar)

	o.logger = log.New()
}

func (o *createClusterOpt) Validate() error {
	errs := []error{}
	if len(o.Name) == 0 {
		errs = append(errs, fmt.Errorf("cluster name is required - specify with %s", clusterNameEnvVar))
	}
	if len(o.Namespace) == 0 {
		errs = append(errs, fmt.Errorf("cluster namespace is required - specify with %s", clusterNamespaceEnvVar))
	}
	if len(o.SSHPublicKeyFile) == 0 {
		errs = append(errs, fmt.Errorf("SSH public key file is required - specify with %s", sshPublicKeyFileEnvVar))
	}
	if len(o.PullSecretFile) == 0 {
		errs = append(errs, fmt.Errorf("pull secret file is required - specify with %s", pullSecretFileEnvVar))
	}
	if len(o.ReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("release image is required - specify with %s", releaseImageEnvVar))
	}
	if len(o.HiveImage) == 0 {
		errs = append(errs, fmt.Errorf("hive image is required - specify with %s", hiveImageEnvVar))
	}
	if len(o.BaseDomain) == 0 {
		errs = append(errs, fmt.Errorf("base domain is required - specify with %s", baseDomainEnvVar))
	}
	if len(os.Getenv(awsSecretAccessKeyEnvVar)) == 0 {
		errs = append(errs, fmt.Errorf("AWS secret access key is required - specify with %s", awsSecretAccessKeyEnvVar))
	}
	if len(os.Getenv(awsSecretKeyIDEnvVar)) == 0 {
		errs = append(errs, fmt.Errorf("AWS secret key ID is required - specify with %s", awsSecretKeyIDEnvVar))
	}

	return nil
}

func (o *createClusterOpt) Run() error {
	log.SetLevel(log.InfoLevel)
	c, err := GetClient()
	if err != nil {
		return err
	}

	err = o.ensureNamespace(c, o.Namespace)
	if err != nil {
		return err
	}

	err = o.createClusterDeployment()
	if err != nil {
		return err
	}

	errChan := make(chan error)
	doneChan := make(chan struct{})
	stopChan := make(chan struct{})
	err = o.followInstall(stopChan, doneChan, errChan)
	if err != nil {
		return err
	}
	defer func() { stopChan <- struct{}{} }()
	select {
	case err := <-errChan:
		log.Errorf("Received an error in the error channel: %v", err)
		return err

	case <-time.After(testTimeout):
		return fmt.Errorf("Timed out waiting for ClusterDeployment to install")

	case <-doneChan:
		o.logger.Info("Cluster has installed successfully")
	}
	return nil
}

func (o *createClusterOpt) ensureNamespace(c client.Client, namespace string) error {
	o.logger.Infof("Ensuring namespace %s exists", namespace)
	ns := &corev1.Namespace{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: namespace}, ns)
	switch {
	case err == nil:
		o.logger.Infof("Namespace %s already exists", namespace)
		return nil
	case errors.IsNotFound(err):
		ns.Name = namespace
		o.logger.Infof("Namespace %s does not exist, creating", namespace)
		err = c.Create(context.TODO(), ns)
		if err != nil {
			return fmt.Errorf("failed to create namespace: %v", err)
		}
	case err != nil:
		return fmt.Errorf("failed to get cluster namespace: %v", err)
	}
	return nil
}

func (o *createClusterOpt) createClusterDeployment() error {
	opt := createcluster.Options{
		Name:             o.Name,
		Namespace:        o.Namespace,
		SSHPublicKeyFile: o.SSHPublicKeyFile,
		BaseDomain:       o.BaseDomain,
		PullSecretFile:   o.PullSecretFile,
		HiveImage:        o.HiveImage,
		ReleaseImage:     o.ReleaseImage,
		IncludeSecrets:   true,
	}
	o.logger.Infof("Creating cluster deployment %s/%s", o.Namespace, o.Name)
	if err := opt.Run(); err != nil {
		return fmt.Errorf("failed to create cluster deployment: %v", err)
	}
	return nil
}

func (o *createClusterOpt) followInstall(stopChan, doneChan chan struct{}, errChan chan error) error {
	o.logger.Infof("Waiting for cluster to install")
	cfg, err := GetConfig()
	if err != nil {
		return err
	}
	objectCache, err := cache.New(cfg, cache.Options{
		Scheme:    scheme.Scheme,
		Namespace: o.Namespace,
	})
	if err != nil {
		return err
	}

	clusterDeploymentInformer, err := objectCache.GetInformer(&hivev1.ClusterDeployment{})
	if err != nil {
		return err
	}

	jobInformer, err := objectCache.GetInformer(&batchv1.Job{})
	if err != nil {
		return err
	}

	podInformer, err := objectCache.GetInformer(&corev1.Pod{})
	if err != nil {
		return err
	}

	clusterDeploymentInformer.AddEventHandler(
		&clientcache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { o.onClusterDeployment(obj, doneChan, errChan) },
			UpdateFunc: func(oldObj, newObj interface{}) { o.onClusterDeployment(newObj, doneChan, errChan) },
		})

	jobInformer.AddEventHandler(
		&clientcache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { o.onJob(obj, errChan) },
			UpdateFunc: func(oldObj, newObj interface{}) { o.onJob(newObj, errChan) },
		})

	podInformer.AddEventHandler(
		&clientcache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { o.onPod(obj) },
			UpdateFunc: func(oldObj, newObj interface{}) { o.onPod(newObj) },
		})

	return objectCache.Start(stopChan)
}

func (o *createClusterOpt) onClusterDeployment(obj interface{}, doneChan chan struct{}, errChan chan error) {
	cd, ok := obj.(*hivev1.ClusterDeployment)
	if !ok {
		o.logger.Debugf("Object is not of type ClusterDeployment: %#v", obj)
		return
	}
	if cd.Name != o.Name || cd.Namespace != o.Namespace {
		return
	}
	if cd.Status.Installed && len(cd.Status.AdminKubeconfigSecret.Name) > 0 {
		doneChan <- struct{}{}
		return
	}
	for _, condition := range cd.Status.Conditions {
		switch condition.Type {
		case hivev1.ClusterImageSetNotFoundCondition,
			hivev1.ControlPlaneCertificateNotFoundCondition,
			hivev1.DNSNotReadyCondition,
			hivev1.IngressCertificateNotFoundCondition,
			hivev1.InstallFailingCondition,
			hivev1.InstallerImageResolutionFailedCondition,
			hivev1.UnreachableCondition:
			if condition.Status == corev1.ConditionTrue {
				o.logger.Errorf("Unexpected condition: %s", condition.Type)
				errChan <- fmt.Errorf("unexpected condtion: %s, reason: %s, message: %s", condition.Type, condition.Reason, condition.Message)
				return
			}
		}
	}
}

func (o *createClusterOpt) onJob(obj interface{}, errChan chan error) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		o.logger.Debugf("Object is not of type Job: %#v", obj)
	}
	_, isInstallJob := job.Labels["hive.openshift.io/install"]
	if isInstallJob {
		cdName := job.Labels["hive.openshift.io/cluster-deployment-name"]
		if cdName != o.Name {
			return
		}
		o.installJob = job.Name
		o.onInstallJob(job, errChan)
		return
	}
	_, isImageSetJob := job.Labels["hive.openshift.io/imageset"]
	if isImageSetJob {
		cdName := job.Labels["hive.openshift.io/cluster-deployment-name"]
		if cdName != o.Name {
			return
		}
		o.onImageSetJob(job, errChan)
		return
	}

}

func (o *createClusterOpt) onPod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		o.logger.Debugf("Object is not of type Pod: %#v", obj)
	}
	if len(o.installJob) == 0 {
		return
	}
	if pod.Labels["job-name"] != o.installJob {
		return
	}
	o.onInstallPod(pod)
}

func (o *createClusterOpt) onInstallJob(job *batchv1.Job, errChan chan error) {
	if !o.startedInstall {
		o.startedInstall = true
		log.Infof("Install job %s/%s has been created", job.Namespace, job.Name)
		return
	}
	if o.completedInstall {
		return
	}
	if !controllerutils.IsFinished(job) {
		return
	}
	o.completedInstall = true
	if controllerutils.IsSuccessful(job) {
		log.Infof("Install job %s/%s completed successfully", job.Namespace, job.Name)
	} else if controllerutils.IsFailed(job) {
		log.Infof("Install job %s/%s has failed", job.Namespace, job.Name)
		errChan <- fmt.Errorf("install job failed")
	}
}

func (o *createClusterOpt) onImageSetJob(job *batchv1.Job, errChan chan error) {
	if !o.startedImageSet {
		o.startedImageSet = true
		log.Infof("ImageSet job %s/%s has been created", job.Namespace, job.Name)
		return
	}
	if o.completedImageSet {
		return
	}
	if !controllerutils.IsFinished(job) {
		return
	}
	o.completedInstall = true
	if controllerutils.IsSuccessful(job) {
		log.Infof("ImageSet job %s/%s completed successfully", job.Namespace, job.Name)
	} else if controllerutils.IsFailed(job) {
		log.Infof("ImageSet job %s/%s has failed", job.Namespace, job.Name)
		errChan <- fmt.Errorf("imageset job failed")
	}
}

func (o *createClusterOpt) onInstallPod(pod *corev1.Pod) {
	if o.followingLog {
		return
	}

	if pod.Status.Phase != corev1.PodRunning {
		return
	}

	for _, cstatus := range pod.Status.ContainerStatuses {
		if cstatus.Name == "hive" {
			if cstatus.State.Running == nil || cstatus.State.Running.StartedAt.IsZero() {
				return
			}
		}
	}

	o.followingLog = true
	go func() {
		client, err := GetKubeClient()
		if err != nil {
			o.logger.Errorf("Cannot get kube client: %v", err)
			return
		}
		cfg, err := GetConfig()
		if err != nil {
			o.logger.Error("Cannot get client config: %v", err)
		}

		parameterCodec := runtime.NewParameterCodec(scheme.Scheme)
		req := client.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(pod.Name).
			Namespace(pod.Namespace).
			SubResource("exec")
		req.VersionedParams(&corev1.PodExecOptions{
			Container: "hive",
			Command:   []string{"tail", "-f", "/tmp/openshift-install-console.log"},
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, parameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
		if err != nil {
			o.logger.Errorf("Cannot get remote command executor: %v", err)
			return
		}

		reader, output := io.Pipe()
		go func() {
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				o.logger.Infof("install log: %s", scanner.Text())
			}
		}()

		exec.Stream(remotecommand.StreamOptions{
			Stdin:  nil,
			Stdout: output,
			Stderr: output,
			Tty:    false,
		})
	}()
}
