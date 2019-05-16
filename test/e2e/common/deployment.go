package common

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	kclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func WaitForDeploymentReady(c kclient.Interface, namespace, name string, timeOut time.Duration) error {
	logger := log.WithField("deployment", fmt.Sprintf("%s/%s", namespace, name))
	logger.Infof("Waiting for deployment to become ready")
	stop := make(chan struct{})
	ready := make(chan struct{})
	onObject := func(obj interface{}) {
		deployment, ok := obj.(*appsv1.Deployment)
		if !ok {
			logger.Warningf("object not deployment: %v", obj)
		}
		log.Debug("determining if deployment is ready")
		for _, condition := range deployment.Status.Conditions {
			if condition.Type == appsv1.DeploymentAvailable {
				if condition.Status == corev1.ConditionTrue {
					logger.Debug("deployment is ready")
					ready <- struct{}{}
					return
				}
			}
		}
		logger.Debug("deployment is not yet ready")
	}
	watchList := cache.NewListWatchFromClient(c.AppsV1().RESTClient(), "deployments", namespace, fields.OneTermEqualSelector("metadata.name", name))
	_, controller := cache.NewInformer(
		watchList,
		&appsv1.Deployment{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: onObject,
			UpdateFunc: func(old, new interface{}) {
				onObject(new)
			},
		},
	)
	go controller.Run(stop)
	defer func() { stop <- struct{}{} }()

	select {
	case <-time.After(timeOut):
		return fmt.Errorf("timed out waiting for deployment %s/%s to be ready", namespace, name)
	case <-ready:
	}
	return nil
}
