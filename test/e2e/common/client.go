package common

import (
	log "github.com/sirupsen/logrus"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	hiveapis "github.com/openshift/hive/pkg/apis"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	kclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

func init() {
	apiextv1beta1.AddToScheme(scheme.Scheme)
	hiveapis.AddToScheme(scheme.Scheme)
}

func GetClient() (client.Client, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	return client.New(config, client.Options{Scheme: scheme.Scheme})
}

func MustGetClient() client.Client {
	c, err := GetClient()
	if err != nil {
		log.Fatalf("Error obtaining client: %v", err)
	}
	return c
}

func GetKubernetesClient() (kclient.Interface, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	return kclient.NewForConfig(config)
}

func MustGetKubernetesClient() kclient.Interface {
	c, err := GetKubernetesClient()
	if err != nil {
		log.Fatalf("Error obtaining kubernetes client: %v", err)
	}
	return c
}
