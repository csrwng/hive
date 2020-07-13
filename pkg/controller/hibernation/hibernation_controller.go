package hibernation

import (
	"context"
	"fmt"
	"time"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	certsv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeclient "k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1"
	hivemetrics "github.com/openshift/hive/pkg/controller/metrics"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
	"github.com/openshift/hive/pkg/remoteclient"
)

const (
	// ControllerName is the name of this controller
	ControllerName = "hibernation"

	// stateCheckInterval is the time interval for polling
	// whether a cluster's machines are stopped or are running
	stateCheckInterval = 30 * time.Second

	// csrCheckInterval is the time interval for polling
	// pending CSRs
	csrCheckInterval = 10 * time.Second
)

var (
	// minimumClusterVersion is the minimum supported version for
	// hibernation
	minimumClusterVersion = semver.MustParse("4.4.8")

	// actuators is a list of available actuators for this controller
	// It is populated via the RegisterActuator function
	actuators []HibernationActuator
)

// Add creates a new Hibernation controller and adds it to the manager with default RBAC.
func Add(mgr manager.Manager) error {
	return AddToManager(mgr, NewReconciler(mgr))
}

// RegisterActuator register an actuator with this controller. The actuator
// determines whether it can handle a particular cluster deployment via the CanHandle
// function.
func RegisterActuator(a HibernationActuator) {
	actuators = append(actuators, a)
}

// hibernationReconciler is the reconciler type for this controller
type hibernationReconciler struct {
	client.Client
	scheme *runtime.Scheme
	logger log.FieldLogger

	remoteClientBuilder func(cd *hivev1.ClusterDeployment) remoteclient.Builder
}

// NewReconciler returns a new Reconciler
func NewReconciler(mgr manager.Manager) *hibernationReconciler {
	logger := log.WithField("controller", ControllerName)
	r := &hibernationReconciler{
		Client: controllerutils.NewClientWithMetricsOrDie(mgr, ControllerName),
		scheme: mgr.GetScheme(),
		logger: logger,
	}
	r.remoteClientBuilder = func(cd *hivev1.ClusterDeployment) remoteclient.Builder {
		return remoteclient.NewBuilder(r.Client, cd, ControllerName)
	}
	return r
}

// AddToManager adds a new Controller to the controller manager
func AddToManager(mgr manager.Manager, r *hibernationReconciler) error {
	c, err := controller.New("hibernation-controller", mgr, controller.Options{Reconciler: r, MaxConcurrentReconciles: controllerutils.GetConcurrentReconciles()})
	if err != nil {
		log.WithField("controller", ControllerName).WithError(err).Error("Error getting new cluster deployment")
		return err
	}

	// Watch for changes to ClusterDeployment
	err = c.Watch(&source.Kind{Type: &hivev1.ClusterDeployment{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		log.WithField("controller", ControllerName).WithError(err).Error("Error watching cluster deployment")
		return err
	}
	return nil
}

// Reconcile syncs a single ClusterDeployment
func (r *hibernationReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	start := time.Now()
	cdLog := r.logger.WithFields(log.Fields{
		"controller":        ControllerName,
		"clusterDeployment": request.NamespacedName.String(),
	})

	cdLog.Info("reconciling cluster deployment")
	defer func() {
		dur := time.Since(start)
		hivemetrics.MetricControllerReconcileTime.WithLabelValues(ControllerName).Observe(dur.Seconds())
		cdLog.WithField("elapsed", dur).Info("reconcile complete")
	}()

	// Fetch the ClusterDeployment instance
	cd := &hivev1.ClusterDeployment{}
	err := r.Get(context.TODO(), request.NamespacedName, cd)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			cdLog.Info("cluster deployment Not Found")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		cdLog.WithError(err).Error("Error getting cluster deployment")
		return reconcile.Result{}, err
	}

	// If cluster is already deleted, skip any processing
	if cd.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	// If cluster is not installed, skip any processing
	if !cd.Spec.Installed {
		return reconcile.Result{}, nil
	}

	shouldHibernate := (cd.Spec.PowerState == hivev1.HibernatingClusterPowerState)
	hibernatingCondition := controllerutils.FindClusterDeploymentCondition(cd.Status.Conditions, hivev1.ClusterHibernatingCondition)

	if !shouldHibernate {
		if hibernatingCondition == nil || hibernatingCondition.Status == corev1.ConditionFalse {
			return reconcile.Result{}, nil
		}
		switch hibernatingCondition.Reason {
		case hivev1.StoppingHibernationReason, hivev1.HibernatingHibernationReason:
			return r.startMachines(cdLog, cd)
		case hivev1.ResumingHibernationReason:
			return r.checkClusterResumed(cdLog, cd)
		}
		return reconcile.Result{}, nil
	}

	if supported, msg := r.canHibernate(cd); !supported {
		return r.setUnsupportedCondition(cdLog, cd, msg)
	}
	if hibernatingCondition == nil || hibernatingCondition.Status == corev1.ConditionFalse || hibernatingCondition.Reason == hivev1.ResumingHibernationReason {
		return r.stopMachines(cdLog, cd)
	}
	switch hibernatingCondition.Reason {
	case hivev1.StoppingHibernationReason:
		return r.checkClusterStopped(cdLog, cd, false)
	case hivev1.HibernatingHibernationReason:
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) setUnsupportedCondition(logger log.FieldLogger, cd *hivev1.ClusterDeployment, message string) (reconcile.Result, error) {
	oldStatus := cd.Status.DeepCopy()
	existing := controllerutils.FindClusterDeploymentCondition(cd.Status.Conditions, hivev1.ClusterHibernatingCondition)
	if existing == nil {
		cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
			Type:    hivev1.ClusterHibernatingCondition,
			Status:  corev1.ConditionFalse,
			Reason:  hivev1.UnsupportedHibernationReason,
			Message: message,
		})
	} else {
		cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
			cd.Status.Conditions,
			hivev1.ClusterHibernatingCondition,
			corev1.ConditionFalse,
			hivev1.UnsupportedHibernationReason,
			message,
			controllerutils.UpdateConditionIfReasonOrMessageChange,
		)
	}
	if !equality.Semantic.DeepEqual(oldStatus, cd.Status) {
		if err := r.Status().Update(context.TODO(), cd); err != nil {
			logger.Error("Failed to set unsupported condition: %v", err)
			return reconcile.Result{}, errors.Wrap(err, "failed to set unsupported condition")
		}
		logger.Info("Hibernation unsupported condition set on cluster deployment.")
	}
	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) startMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment) (reconcile.Result, error) {
	actuator := r.getActuator(cd)
	if actuator == nil {
		logger.Warning("No compatible actuator found to start cluster machines")
		return reconcile.Result{}, nil
	}
	logger.Info("Resuming cluster")
	if err := actuator.StartMachines(logger, cd, r.Client); err != nil {
		return reconcile.Result{}, err
	}
	oldStatus := cd.Status.DeepCopy()
	cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
		cd.Status.Conditions,
		hivev1.ClusterHibernatingCondition,
		corev1.ConditionTrue,
		hivev1.ResumingHibernationReason,
		"Starting cluster machines",
		controllerutils.UpdateConditionIfReasonOrMessageChange)
	if !equality.Semantic.DeepEqual(oldStatus, cd.Status) {
		if err := r.Status().Update(context.TODO(), cd); err != nil {
			logger.Error("Failed to update status: %v", err)
			return reconcile.Result{}, errors.Wrap(err, "failed to update status")
		}
	}
	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) stopMachines(logger log.FieldLogger, cd *hivev1.ClusterDeployment) (reconcile.Result, error) {
	actuator := r.getActuator(cd)
	if actuator == nil {
		logger.Warning("No compatible actuator found to start cluster machines")
		return reconcile.Result{}, nil
	}
	logger.Info("Stopping cluster")
	if err := actuator.StopMachines(logger, cd, r.Client); err != nil {
		return reconcile.Result{}, err
	}
	oldStatus := cd.Status.DeepCopy()
	cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
		cd.Status.Conditions,
		hivev1.ClusterHibernatingCondition,
		corev1.ConditionTrue,
		hivev1.StoppingHibernationReason,
		"Stopping cluster machines",
		controllerutils.UpdateConditionIfReasonOrMessageChange)
	if !equality.Semantic.DeepEqual(oldStatus, cd.Status) {
		if err := r.Status().Update(context.TODO(), cd); err != nil {
			logger.Error("Failed to update status:", err)
			return reconcile.Result{}, errors.Wrap(err, "failed to update status")
		}
	}
	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) checkClusterStopped(logger log.FieldLogger, cd *hivev1.ClusterDeployment, expectRunning bool) (reconcile.Result, error) {
	actuator := r.getActuator(cd)
	if actuator == nil {
		logger.Warning("No compatible actuator found to check machine status")
		return reconcile.Result{}, nil
	}
	stopped, err := actuator.MachinesStopped(logger, cd, r.Client)
	if err != nil {
		logger.WithError(err).Error("Failed to check whether machines are stopped.")
		return reconcile.Result{}, err
	}
	if !stopped {
		return reconcile.Result{RequeueAfter: stateCheckInterval}, nil
	}
	oldStatus := cd.Status.DeepCopy()
	cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
		cd.Status.Conditions,
		hivev1.ClusterHibernatingCondition,
		corev1.ConditionTrue,
		hivev1.HibernatingHibernationReason,
		"Cluster is stopped",
		controllerutils.UpdateConditionIfReasonOrMessageChange)
	if !equality.Semantic.DeepEqual(oldStatus, cd.Status) {
		if err := r.Status().Update(context.TODO(), cd); err != nil {
			logger.WithError(err).Error("Failed to update status")
			return reconcile.Result{}, errors.Wrap(err, "failed to update status")
		}
	}
	logger.Info("Cluster has stopped and is in hibernating state")
	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) checkClusterResumed(logger log.FieldLogger, cd *hivev1.ClusterDeployment) (reconcile.Result, error) {
	actuator := r.getActuator(cd)
	if actuator == nil {
		logger.Warning("No compatible actuator found to check machine status")
		return reconcile.Result{}, nil
	}
	running, err := actuator.MachinesRunning(logger, cd, r.Client)
	if err != nil {
		logger.WithError(err).Error("Failed to check whether machines are running.")
		return reconcile.Result{}, err
	}
	if !running {
		return reconcile.Result{RequeueAfter: stateCheckInterval}, nil
	}
	ready, unreachable, err := r.nodesReady(logger, cd)
	if err != nil {
		logger.WithError(err).Error("Failed to check whether nodes are ready")
		return reconcile.Result{}, err
	}
	if unreachable {
		logger.Debug("Cluster is still not reachable, waiting")
		return reconcile.Result{RequeueAfter: stateCheckInterval}, nil
	}
	if !ready {
		logger.Info("Nodes are not ready, checking for CSRs to approve")
		return r.checkCSRs(logger, cd)
	}
	oldStatus := cd.Status.DeepCopy()
	cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
		cd.Status.Conditions,
		hivev1.ClusterHibernatingCondition,
		corev1.ConditionFalse,
		hivev1.RunningHibernationReason,
		"All machines are started and nodes are ready",
		controllerutils.UpdateConditionIfReasonOrMessageChange)
	if !equality.Semantic.DeepEqual(oldStatus, cd.Status) {
		if err := r.Status().Update(context.TODO(), cd); err != nil {
			logger.WithError(err).Error("Failed to update condition")
			return reconcile.Result{}, errors.Wrap(err, "failed to set unsupported condition")
		}
	}
	logger.Info("Cluster has started and is in Running state")
	return reconcile.Result{}, nil
}

func (r *hibernationReconciler) getActuator(cd *hivev1.ClusterDeployment) HibernationActuator {
	for _, a := range actuators {
		if a.CanHandle(cd) {
			return a
		}
	}
	return nil
}

func (r *hibernationReconciler) canHibernate(cd *hivev1.ClusterDeployment) (bool, string) {
	if r.getActuator(cd) == nil {
		return false, "Unsupported platform: no actuator to handle it"
	}
	if cd.Status.ClusterVersionStatus == nil || len(cd.Status.ClusterVersionStatus.Desired.Version) == 0 {
		return false, "No cluster version is available yet"
	}
	version, err := semver.Parse(cd.Status.ClusterVersionStatus.Desired.Version)
	if err != nil {
		return false, fmt.Sprintf("Cannot parse server version: %v", err)
	}
	if version.LT(minimumClusterVersion) {
		return false, "Unsupported version, need version 4.4.8 or greater"
	}
	return true, ""
}

func (r *hibernationReconciler) nodesReady(logger log.FieldLogger, cd *hivev1.ClusterDeployment) (ready bool, isUnreachable bool, err error) {
	remoteClient, unreachable, requeue := remoteclient.ConnectToRemoteCluster(
		cd,
		r.remoteClientBuilder(cd),
		r.Client,
		logger,
	)
	if requeue {
		logger.Error("Failed to get client to target cluster")
		err = fmt.Errorf("failed to get client to target cluster")
		return
	}
	if unreachable {
		logger.Debug("Cluster is still unreachable while checking for nodes ready, waiting")
		isUnreachable = true
		return
	}
	nodeList := &corev1.NodeList{}
	err = remoteClient.List(context.TODO(), nodeList)
	if err != nil {
		logger.WithError(err).Error("Failed to fetch cluster nodes")
		err = errors.Wrap(err, "failed to fetch cluster nodes")
		return
	}
	if len(nodeList.Items) == 0 {
		logger.Info("Cluster is not reporting any nodes, waiting")
		return
	}
	for i := range nodeList.Items {
		if !isNodeReady(&nodeList.Items[i]) {
			logger.WithField("node", nodeList.Items[i].Name).Info("Node is not yet ready, waiting")
			return
		}
	}
	logger.WithField("count", len(nodeList.Items)).Info("All cluster nodes are ready")
	ready = true
	return
}

func (r *hibernationReconciler) checkCSRs(logger log.FieldLogger, cd *hivev1.ClusterDeployment) (reconcile.Result, error) {
	kubeClient, unreachable, requeue := remoteclient.ConnectToRemoteClusterWithKubeClient(
		cd,
		r.remoteClientBuilder(cd),
		r.Client,
		logger,
	)
	if requeue {
		logger.Error("Failed to get client to target cluster")
		return reconcile.Result{}, errors.New("failed to get client to target cluster")
	}
	if unreachable {
		logger.Info("Cluster is still not reachable while checking CSRs, wating")
		return reconcile.Result{RequeueAfter: stateCheckInterval}, nil
	}

	csrList, err := kubeClient.CertificatesV1beta1().CertificateSigningRequests().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.WithError(err).Error("failed to list CSRs")
		return reconcile.Result{}, errors.Wrap(err, "failed to list CSRs")
	}
	for i := range csrList.Items {
		if isApproved(&csrList.Items[i]) {
			logger.WithField("csr", csrList.Items[i].Name).Debug("CSR is already approved")
			continue
		}
		if shouldApprove(&csrList.Items[i]) {
			err = approveCSR(kubeClient, &csrList.Items[i])
			if err != nil {
				logger.WithError(err).WithField("csr", csrList.Items[i].Name).Error("Failed to approve CSR")
				return reconcile.Result{}, errors.Wrap(err, "failed to approve CSR")
			}
			logger.WithField("csr", csrList.Items[i].Name).Info("CSR approved")
		} else {
			logger.WithField("csr", csrList.Items[i].Name).Warning("CSR failed validation")
		}
	}
	// Requeue quickly after so we can recheck whether more CSRs need to be approved
	return reconcile.Result{RequeueAfter: csrCheckInterval}, nil
}

func shouldApprove(csr *certsv1beta1.CertificateSigningRequest) bool {
	// TODO: Implement CSR validation
	return true
}

func approveCSR(client kubeclient.Interface, csr *certsv1beta1.CertificateSigningRequest) error {

	// Remove any previous CertificateApproved condition
	newConditions := []certsv1beta1.CertificateSigningRequestCondition{}
	for _, c := range csr.Status.Conditions {
		if c.Type != certsv1beta1.CertificateApproved {
			newConditions = append(newConditions, c)
		}
	}

	// Add approved condition
	newConditions = append(newConditions, certsv1beta1.CertificateSigningRequestCondition{
		Type:           certsv1beta1.CertificateApproved,
		Reason:         "KubectlApprove",
		Message:        "This CSR was automatically approved by Hive",
		LastUpdateTime: metav1.Now(),
	})
	csr.Status.Conditions = newConditions
	_, err := client.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(context.TODO(), csr, metav1.UpdateOptions{})
	return err
}

func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isApproved(csr *certsv1beta1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certsv1beta1.CertificateApproved {
			return true
		}
	}
	return false
}
