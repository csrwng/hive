package clusterstate

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
	hivemetrics "github.com/openshift/hive/pkg/controller/metrics"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
)

const (
	controllerName = "clusterState"
)

var (
	statusUpdateInterval = 10 * time.Minute
)

// Add creates a new ClusterState controller and adds it to the manager with default RBAC.
func Add(mgr manager.Manager) error {
	return AddToManager(mgr, NewReconciler(mgr))
}

// NewReconciler returns a new reconcile.Reconciler
func NewReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileClusterState{
		Client:              controllerutils.NewClientWithMetricsOrDie(mgr, controllerName),
		scheme:              mgr.GetScheme(),
		logger:              log.WithField("controller", controllerName),
		remoteClientBuilder: controllerutils.BuildClusterAPIClientFromKubeconfig,
	}
}

// AddToManager adds a new Controller to mgr with r as the reconcile.Reconciler
func AddToManager(mgr manager.Manager, r reconcile.Reconciler) error {
	c, err := controller.New("clusterstate-controller", mgr, controller.Options{Reconciler: r, MaxConcurrentReconciles: controllerutils.GetConcurrentReconciles()})
	if err != nil {
		log.WithField("controller", controllerName).WithError(err).Error("Error creating new clusterstate controller")
		return err
	}

	// Watch for changes to ClusterState
	err = c.Watch(&source.Kind{Type: &hivev1.ClusterState{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		log.WithField("controller", controllerName).WithError(err).Error("Error watching cluster deployment")
		return err
	}

	// Watch for changes to ClusterDeployment
	err = c.Watch(&source.Kind{Type: &hivev1.ClusterDeployment{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(object handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{Namespace: object.Meta.GetNamespace(), Name: object.Meta.GetName()},
				},
			}
		}),
	})

	return nil
}

type ReconcileClusterState struct {
	client.Client
	scheme *runtime.Scheme
	logger log.FieldLogger

	// remoteClientBuilder is a function pointer to the function that builds a client for the
	// remote cluster
	remoteClientBuilder func(string, string) (client.Client, error)
}

// +kubebuilder:rbac:groups=hive.openshift.io,resources=clusterstate;clusterstate/status,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileClusterState) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	start := time.Now()
	logger := r.logger.WithFields(log.Fields{
		"controller":   controllerName,
		"clusterState": request.NamespacedName.String(),
	})

	// For logging, we need to see when the reconciliation loop starts and ends.
	logger.Info("reconciling cluster state")
	defer func() {
		dur := time.Since(start)
		hivemetrics.MetricControllerReconcileTime.WithLabelValues(controllerName).Observe(dur.Seconds())
		logger.WithField("elapsed", dur).Info("reconcile complete")
	}()

	// Fetch the ClusterState instance
	st := &hivev1.ClusterState{}
	err := r.Get(context.TODO(), request.NamespacedName, st)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			logger.Info("cluster state not found")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.WithError(err).Error("Error getting cluster state")
		return reconcile.Result{}, err
	}
	if !st.DeletionTimestamp.IsZero() {
		logger.Infof("ClusterState resource has been deleted")
		return reconcile.Result{}, nil
	}

	// Fetch corresponding ClusterDeployment instance
	cd := &hivev1.ClusterDeployment{}
	err = r.Get(context.TODO(), types.NamespacedName{Name: st.Spec.ClusterDeployment.Name, Namespace: st.Namespace}, cd)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("cluster deployment not found")
			return reconcile.Result{}, nil
		}
		logger.WithError(err).Error("Error getting cluster deployment")
		return reconcile.Result{}, err
	}
	if !cd.DeletionTimestamp.IsZero() {
		logger.Infof("ClusterDeployment resource has been deleted")
		return reconcile.Result{}, nil
	}
	if !cd.Status.Installed || len(cd.Status.AdminKubeconfigSecret.Name) == 0 {
		logger.Infof("ClusterDeployment is not ready")
		return reconcile.Result{}, nil
	}
	kubeconfigSecret := &corev1.Secret{}
	err = r.Get(context.Background(), types.NamespacedName{Namespace: cd.Namespace, Name: cd.Status.AdminKubeconfigSecret.Name}, kubeconfigSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Warn("admin kubeconfig does not yet exist")
		} else {
			return reconcile.Result{}, err
		}
	}
	kubeconfig, err := controllerutils.FixupKubeconfigSecretData(kubeconfigSecret.Data)
	remoteClient, err := r.remoteClientBuilder(string(kubeconfig), controllerName)
	if err != nil {
		logger.WithError(err).Error("error building remote cluster client connection")
		return reconcile.Result{}, err
	}

	clusterOperators := &configv1.ClusterOperatorList{}
	err = remoteClient.List(context.TODO(), clusterOperators)
	if err != nil {
		logger.WithError(err).Errorf("failed to list target cluster operators")
		return reconcile.Result{}, err
	}

	operatorStates := make([]hivev1.ClusterOperatorState, 0, len(clusterOperators.Items))
	for _, clusterOperator := range clusterOperators.Items {
		operatorStates = append(operatorStates, hivev1.ClusterOperatorState{
			Name:       clusterOperator.Name,
			Conditions: clusterOperator.Status.Conditions,
		})
	}
	st.Status.ClusterOperators = operatorStates
	err = r.Status().Update(context.Background(), st)
	if err != nil {
		logger.WithError(err).Errorf("failed to update cluster operator state")
	}
	return reconcile.Result{
		RequeueAfter: statusUpdateInterval,
	}, nil
}
