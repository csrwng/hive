package syncset

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/pkg/apis/hive/v1alpha1"
	"github.com/openshift/hive/test/e2e/common"
)

func TestSyncSets(t *testing.T) {

	/*
	   Tests to execute:
	   SyncSet vs SelectorSyncSet
	     SelectorSyncSet (matches vs don't match)
	   Resources/None vs Resources/Upsert vs Resources/Sync
	   Secret References (None vs Present)
	   Patches (None vs 1 vs >1)
	     Patch Mode (3 modes)
	   Resource with Error, no Error
	   Patch with Error, no Error
	   Secret with Error, no Error
	*/

	tests := []struct {
		name     string
		syncsets []*hivev1.SyncSet
	}{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

		})
	}
}

func TestSyncSetWithResources(t *testing.T) {
	client, ns := clientAndNamespace()
	ss := syncSet(ns, "resources-test",
		withNamespace("test1"),
		withConfigMap("test1", "foo", "key1=value1"),
		withConfigMap("test1", "bar", "key2=value2"))
	err := client.Create(context.TODO(), ss)
	assert.Nil(t, err)
}

func clientAndNamespace() (client.Client, string) {
	cd := common.MustGetInstalledClusterDeployment()
	namespace := cd.Namespace
	c := common.MustGetClient()
	return c, namespace
}

func syncSet(namespace, name string, optionFuncs ...func(*hivev1.SyncSet)) *hivev1.SyncSet {
	ss := &hivev1.SyncSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	for _, f := range optionFuncs {
		f(ss)
	}
	return ss
}

func withNamespace(namespace string) func(*hivev1.SyncSet) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	return func(ss *hivev1.SyncSet) {
		ss.Spec.Resources = append(ss.Spec.Resources, runtime.RawExtension{Object: ns})
	}
}

func withConfigMap(namespace, name string, data ...string) func(*hivev1.SyncSet) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{},
	}
	for _, value := range data {
		parts := strings.SplitN(value, "=", 2)
		k := parts[0]
		v := ""
		if len(parts) > 1 {
			v = parts[1]
		}
		cm.Data[k] = v
	}

	return func(ss *hivev1.SyncSet) {
		ss.Spec.Resources = append(ss.Spec.Resources, runtime.RawExtension{Object: cm})
	}

}
