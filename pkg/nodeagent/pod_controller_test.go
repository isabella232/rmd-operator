package nodeagent

import (
	"context"
	"fmt"
	"github.com/intel/rmd-operator/pkg/apis"
	intelv1alpha1 "github.com/intel/rmd-operator/pkg/apis/intel/v1alpha1"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"os"
	"path/filepath"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"testing"
)

func createReconcilePodObject(pod *corev1.Pod) (*ReconcilePod, error) {
	// Register operator types with the runtime scheme.
	s := scheme.Scheme

	// Add route Openshift scheme
	if err := apis.AddToScheme(s); err != nil {
		return nil, err
	}

	// Objects to track in the fake client.
	objs := []runtime.Object{pod}

	// Register operator types with the runtime scheme.
	s.AddKnownTypes(intelv1alpha1.SchemeGroupVersion)

	// Create a fake client to mock API calls.
	cl := fake.NewFakeClient(objs...)

	// Create a ReconcileNode object with the scheme and fake client.
	r := &ReconcilePod{client: cl, scheme: s}

	return r, nil

}

func createMockCgroupfs(base string, podUID string, containerID string, t *testing.T) string {
	path := fmt.Sprintf("%s%s%s%s%s%s", base, "/kubepods.slice/pod", podUID, ".slice/docker-", containerID, ".slice")
	err := os.MkdirAll(path, 0666)
	if err != nil {
		t.Fatalf("MkdirAll %q: %s", path, err)
	}
	return path
}

func createMockSystemdfs(base string, podUID string, containerID string, t *testing.T) string {
	podUID = strings.Replace(podUID, "-", "_", -1)
	path := fmt.Sprintf("%s%s%s%s%s%s", base, "/kubepods.slice/pod", podUID, ".slice/docker-", containerID, ".slice")
	err := os.MkdirAll(path, 0666)
	if err != nil {
		t.Fatalf("MkdirAll %q: %s", path, err)
	}
	return path
}

func TestPodControllerReconcile(t *testing.T) {
	tcases := []struct {
		name                string
		pod                 *corev1.Pod
		cores               string
		nodeAgentPodList    *corev1.PodList
		nodeAgentPodName    string
		namespaceList       *corev1.NamespaceList
		expectedRmdWorkload *intelv1alpha1.RmdWorkload
		errorExpected       bool
	}{
		{
			name: "test case 1 - pod pending",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
					HostIP: "10.0.0.1",
					Phase:  "Pending",
				},
			},
			cores: "0",
			nodeAgentPodList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node3",
							Namespace: "prod",
						},
						Status: corev1.PodStatus{
							HostIP: "10.0.0.1",
						},
					},
				},
			},
			nodeAgentPodName: "rmd-node-agent-node3",
			namespaceList: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod",
							Namespace: "prod",
						},
					},
				},
			},
			expectedRmdWorkload: nil,
			errorExpected:       true,
		},
		{
			name: "test case 2 - pod not on same host",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
					HostIP: "10.0.0.101",
					Phase:  "Running",
				},
			},
			cores: "0",
			nodeAgentPodList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node-3",
							Namespace: "default",
						},
						Status: corev1.PodStatus{
							HostIP: "10.0.0.1",
						},
					},
				},
			},
			nodeAgentPodName: "rmd-node-agent-node-3",
			namespaceList: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
					},
				},
			},
			expectedRmdWorkload: nil,
			errorExpected:       false,
		},

		{
			name: "test case 3 - all fields with cache",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
					UID:       "f906a249-ab9d-4180-9afa-4075e2058ac7",
					Annotations: map[string]string{
						"nginx1_policy":    "gold",
						"nginx1_cache_min": "2",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "example-node-1.com",
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
					HostIP: "10.0.0.1",
					Phase:  "Running",
				},
			},
			cores: "0",
			nodeAgentPodList: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node1",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node2",
							Namespace: "test",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node3",
							Namespace: "prod",
						},
						Status: corev1.PodStatus{
							HostIP: "10.0.0.1",
						},
					},
				},
			},
			nodeAgentPodName: "rmd-node-agent-node3",
			namespaceList: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "test",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod",
							Namespace: "prod",
						},
					},
				},
			},
			expectedRmdWorkload: &intelv1alpha1.RmdWorkload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rmd-workload-pod-1",
					Namespace: "default",
				},
				Spec: intelv1alpha1.RmdWorkloadSpec{
					Nodes:   []string{"example-node-1.com"},
					CoreIds: []string{"0"},
					Policy:  "gold",
					Cache: intelv1alpha1.Cache{
						Max: 2,
						Min: 2,
					},
				},
			},
			errorExpected: false,
		},
	}

	for _, tc := range tcases {
		// Create a ReconcileNode object with the scheme and fake client.
		r, err := createReconcilePodObject(tc.pod)
		if err != nil {
			t.Fatalf("error creating ReconcileRmdNodeState object: (%v)", err)
		}

		podName := tc.pod.GetObjectMeta().GetName()
		podNamespace := tc.pod.GetObjectMeta().GetNamespace()
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      podName,
				Namespace: podNamespace,
			},
		}
		content := []byte(tc.cores)
		unifiedCgroupPath = "./test_cgroup/"
		podUID := string(tc.pod.GetObjectMeta().GetUID())
		containerID := tc.pod.Status.ContainerStatuses[0].ContainerID
		dir := createMockCgroupfs(unifiedCgroupPath, podUID, containerID, t)
		tmpfn := filepath.Join(dir, "cpuset.cpus")
		if err := ioutil.WriteFile(tmpfn, content, 0666); err != nil {
			t.Fatalf("error writing to file (%v)", err)
		}
		for i := range tc.namespaceList.Items {
			err = r.client.Create(context.TODO(), &tc.namespaceList.Items[i])
			if err != nil {
				t.Fatalf("could not create namespace: (%v)", err)
			}
		}
		for i := range tc.nodeAgentPodList.Items {
			err = r.client.Create(context.TODO(), &tc.nodeAgentPodList.Items[i])
			if err != nil {
				t.Fatalf("could not create node agent pod: (%v)", err)
			}
		}
		os.Setenv("POD_NAME", tc.nodeAgentPodName)
		errorReturned := false
		res, err := r.Reconcile(req)
		if err != nil {
			errorReturned = true
		}
		os.RemoveAll("./test_cgroup")

		//Check the result of reconciliation to make sure it has the desired state.
		if res.Requeue {
			t.Error("reconcile unexpectedly requeued request")
		}
		rmdWorkload := &intelv1alpha1.RmdWorkload{}
		if tc.expectedRmdWorkload != nil {
			rmdWorkloadName := tc.expectedRmdWorkload.GetObjectMeta().GetName()
			err = r.client.Get(context.TODO(), types.NamespacedName{
				Name: rmdWorkloadName, Namespace: req.Namespace}, rmdWorkload)
			if err != nil {
				t.Fatalf("could not get workload: (%v)", err)
			}

			if !reflect.DeepEqual(tc.expectedRmdWorkload.Spec, rmdWorkload.Spec) {
				t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.expectedRmdWorkload, rmdWorkload)
			}
		}
		if errorReturned != tc.errorExpected {
			t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.errorExpected, errorReturned)
		}

	}
}

func TestReadCgroupCpuset(t *testing.T) {
	tcases := []struct {
		name            string
		podUID          string
		containerID     string
		cores           string
		expectedCoreIDs []string
	}{
		{
			name:            "test case 1",
			podUID:          "0ae5c03d-5fb3-4eb9-9de8-2bd4b51606ba",
			containerID:     "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38c66",
			cores:           "0,21",
			expectedCoreIDs: []string{"0", "21"},
		},
		{
			name:            "test case 2",
			podUID:          "6aaa09a1-241a-4013-b706-fe80ae371206",
			containerID:     "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38c66",
			cores:           "0-5",
			expectedCoreIDs: []string{"0", "1", "2", "3", "4", "5"},
		},
		{
			name:            "test case 3",
			podUID:          "f906a249-ab9d-4180-9afa-4075e2058ac7",
			containerID:     "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38c66",
			cores:           "",
			expectedCoreIDs: []string{},
		},
	}
	for _, tc := range tcases {
		content := []byte(tc.cores)
		unifiedCgroupPath = "./test_cgroup/"
		legacyCgroupPath = "./test_cgroup/cpuset/"
		hybridCgroupPath = "./test_cgroup/unified"
		cgroupPaths := []string{unifiedCgroupPath, legacyCgroupPath, hybridCgroupPath}
		for _, cgroupPath := range cgroupPaths {
			dir := createMockCgroupfs(cgroupPath, tc.podUID, tc.containerID, t)
			tmpfn := filepath.Join(dir, "cpuset.cpus")
			if err := ioutil.WriteFile(tmpfn, content, 0666); err != nil {
				t.Fatalf("error writing to file (%v)", err)
			}
			coreIDs, err := readCgroupCpuset(tc.podUID, tc.containerID)
			if err != nil {
				t.Errorf("error reading cgroups cpuset (%v)", err)
			}
			if !reflect.DeepEqual(tc.expectedCoreIDs, coreIDs) {
				t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.expectedCoreIDs, coreIDs)
			}
		}
		os.RemoveAll("./test_cgroup")
		for _, cgroupPath := range cgroupPaths {
			dir := createMockSystemdfs(cgroupPath, tc.podUID, tc.containerID, t)
			tmpfn := filepath.Join(dir, "cpuset.cpus")
			if err := ioutil.WriteFile(tmpfn, content, 0666); err != nil {
				t.Fatalf("error writing to file (%v)", err)
			}
			coreIDs, err := readCgroupCpuset(tc.podUID, tc.containerID)
			if err != nil {
				t.Errorf("error reading cgroups cpuset (%v)", err)
			}
			if !reflect.DeepEqual(tc.expectedCoreIDs, coreIDs) {
				t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.expectedCoreIDs, coreIDs)
			}
		}
		os.RemoveAll("./test_cgroup")
	}

}

func TestBuildRmdWorkload(t *testing.T) {
	tcases := []struct {
		name                string
		pod                 *corev1.Pod
		cores               string
		expectedRmdWorkload *intelv1alpha1.RmdWorkload
	}{
		{
			name: "test case 1 - all fields with cache",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
					UID:       "f906a249-ab9d-4180-9afa-4075e2058ac7",
					Annotations: map[string]string{
						"nginx1_policy":    "gold",
						"nginx1_cache_min": "2",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "example-node-1.com",
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
				},
			},
			cores: "0",
			expectedRmdWorkload: &intelv1alpha1.RmdWorkload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rmd-workload-pod-1",
					Namespace: "default",
				},
				Spec: intelv1alpha1.RmdWorkloadSpec{
					Nodes:   []string{"example-node-1.com"},
					CoreIds: []string{"0"},
					Policy:  "gold",
					Cache: intelv1alpha1.Cache{
						Max: 2,
						Min: 2,
					},
				},
			},
		},
		{
			name: "test case 2 - all fields with cache and pstate",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
					UID:       "f906a249-ab9d-4180-9afa-4075e2058ac7",
					Annotations: map[string]string{
						"nginx1_policy":            "gold",
						"nginx1_cache_min":         "2",
						"nginx1_pstate_monitoring": "on",
						"nginx1_pstate_ratio":      "1.5",
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "example-node-1.com",
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
				},
			},
			cores: "0,11",
			expectedRmdWorkload: &intelv1alpha1.RmdWorkload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rmd-workload-pod-1",
					Namespace: "default",
				},
				Spec: intelv1alpha1.RmdWorkloadSpec{
					Nodes:   []string{"example-node-1.com"},
					CoreIds: []string{"0", "11"},
					Policy:  "gold",
					Cache: intelv1alpha1.Cache{
						Max: 2,
						Min: 2,
					},
					Pstate: intelv1alpha1.Pstate{
						Monitoring: "on",
						Ratio:      "1.5",
					},
				},
			},
		},
		{
			name: "test case 3 - malformed fields",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
					UID:       "f906a249-ab9d-4180-9afa-4075e2058ac7",
					Annotations: map[string]string{
						"policy":                  "gold", // missing container name prefix
						"nginx1_cache_min":        "2",    // ok
						"nginx1_state_monitoring": "on",   //should be 'pstate'
						"nginx1_pstate_ratiox":    "1.5",  //trailing 'x'
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "example-node-1.com",
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "7479d8c641a73fced579a3517b6d2def3f0a3a3a7e659f86ce4db61dc9f38",
						},
					},
				},
			},
			cores: "8-12",
			expectedRmdWorkload: &intelv1alpha1.RmdWorkload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rmd-workload-pod-1",
					Namespace: "default",
				},
				Spec: intelv1alpha1.RmdWorkloadSpec{
					Nodes:   []string{"example-node-1.com"},
					CoreIds: []string{"8", "9", "10", "11", "12"},
					Cache: intelv1alpha1.Cache{
						Max: 2,
						Min: 2,
					},
				},
			},
		},
	}
	for _, tc := range tcases {
		content := []byte(tc.cores)
		unifiedCgroupPath = "./test_cgroup/"
		podUID := string(tc.pod.GetObjectMeta().GetUID())
		containerID := tc.pod.Status.ContainerStatuses[0].ContainerID
		dir := createMockCgroupfs(unifiedCgroupPath, podUID, containerID, t)
		tmpfn := filepath.Join(dir, "cpuset.cpus")
		if err := ioutil.WriteFile(tmpfn, content, 0666); err != nil {
			t.Fatalf("error writing to file (%v)", err)
		}
		defer os.RemoveAll("./test_cgroup")

		rmdWorkload, err := buildRmdWorkload(tc.pod)
		if err != nil {
			t.Errorf("Failed: %v", err)
		}
		if !reflect.DeepEqual(tc.expectedRmdWorkload, rmdWorkload) {
			t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.expectedRmdWorkload, rmdWorkload)
		}

	}

}

func TestGetContainerRequestingCache(t *testing.T) {
	tcases := []struct {
		name         string
		pod          *corev1.Pod
		container    *corev1.Container
		cacheRequest int
	}{
		{
			name: "test case 1 - single contianer requesting cache",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			container: &corev1.Container{
				Name: "nginx1",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
					},
				},
			},
			cacheRequest: 2,
		},

		{
			name: "test case 2 - 2 containers, 1 requesting cache",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):    resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory): resource.MustParse("1G"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):    resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory): resource.MustParse("1G"),
								},
							},
						},

						{
							Name: "nginx2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			container: &corev1.Container{
				Name: "nginx2",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("2"),
					},
				},
			},

			cacheRequest: 2,
		},
		{
			name: "test case 3 - 2 containers, 2 requesting cache. Only consider 1st ",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
							},
						},

						{
							Name: "nginx2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			container: &corev1.Container{
				Name: "nginx1",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
				},
			},

			cacheRequest: 3,
		},
		{
			name: "test case 4 - no container requesting cache",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName("intel.com/qat_generic"): resource.MustParse("2"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName("intel.com/qat_generic"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			container:    nil,
			cacheRequest: 0,
		},
	}
	for _, tc := range tcases {
		container, cacheRequest := getContainerRequestingCache(tc.pod)
		if !reflect.DeepEqual(container, tc.container) || cacheRequest != tc.cacheRequest {
			t.Errorf("Failed: %v - Expected\n %v\n and \n%v\n, got \n%v\n and \n%v\n", tc.name, tc.container, tc.cacheRequest, container, cacheRequest)
		}

	}

}

func TestGetContainerID(t *testing.T) {
	tcases := []struct {
		name          string
		pod           *corev1.Pod
		containerName string
		containerID   string
	}{
		{
			name: "test case 1 - single container",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "abcd-efgh-ijkl-mnop",
						},
					},
				},
			},
			containerName: "nginx1",
			containerID:   "abcd-efgh-ijkl-mnop",
		},
		{
			name: "test case 2 - multiple containers, one matches",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx0",
							ContainerID: "abcd-efgh-ijkl-mnop",
						},
						{
							Name:        "nginx1",
							ContainerID: "abcd-efgh-ijkl-qrst",
						},
						{
							Name:        "nginx2",
							ContainerID: "abcd-efgh-ijkl-uvwx",
						},
					},
				},
			},
			containerName: "nginx1",
			containerID:   "abcd-efgh-ijkl-qrst",
		},
		{
			name: "test case 3 - multiple containers, none match",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:        "nginx1",
							ContainerID: "abcd-efgh-ijkl-mnop",
						},
						{
							Name:        "nginx2",
							ContainerID: "abcd-efgh-ijkl-qrst",
						},
						{
							Name:        "nginx3",
							ContainerID: "abcd-efgh-ijkl-uvwx",
						},
					},
				},
			},
			containerName: "nginx",
			containerID:   "",
		},
	}
	for _, tc := range tcases {
		containerID := getContainerID(tc.pod, tc.containerName)
		if containerID != tc.containerID {
			t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.containerID, containerID)
		}
	}
}

func TestExclusiveCPUs(t *testing.T) {
	tcases := []struct {
		name      string
		pod       *corev1.Pod
		container *corev1.Container
		expected  bool
	}{
		{
			name: "test case 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse(".5"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse(".5"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
							},
						},

						{
							Name: "nginx2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			container: &corev1.Container{
				Name: "nginx1",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse(".5"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse(".5"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
				},
			},
			expected: false,
		},
		{
			name: "test case 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
								},
							},
						},

						{
							Name: "nginx2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
									corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
									corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			container: &corev1.Container{
				Name: "nginx1",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceName(corev1.ResourceCPU):        resource.MustParse("1"),
						corev1.ResourceName(corev1.ResourceMemory):     resource.MustParse("1G"),
						corev1.ResourceName("intel.com/l3_cache_ways"): resource.MustParse("3"),
					},
				},
			},
			expected: true,
		},
	}
	for _, tc := range tcases {
		result := exclusiveCPUs(tc.pod, tc.container)
		if result != tc.expected {
			t.Errorf("Failed: %v - Expected %v, got %v", tc.name, tc.expected, result)
		}

	}

}

func TestGetNodeAgent(t *testing.T) {
	tcases := []struct {
		name                  string
		pod                   *corev1.Pod
		nodeAgentlist         *corev1.PodList
		nodeAgentpodName      string
		namespacelist         *corev1.NamespaceList
		expectedNamespaceName types.NamespacedName
	}{
		{
			name: "test case 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			nodeAgentlist: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-xyz",
							Namespace: "default",
						},
					},
				},
			},
			nodeAgentpodName: "rmd-node-agent-xyz",
			namespacelist: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default",
							Namespace: "default",
						},
					},
				},
			},
			expectedNamespaceName: types.NamespacedName{
				Name:      "rmd-node-agent-xyz",
				Namespace: "default",
			},
		},
		{
			name: "test case 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			nodeAgentlist: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node1",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node2",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node3",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node4",
							Namespace: "default",
						},
					},
				},
			},
			nodeAgentpodName: "rmd-node-agent-node3",
			namespacelist: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default",
							Namespace: "default",
						},
					},
				},
			},
			expectedNamespaceName: types.NamespacedName{
				Name:      "rmd-node-agent-node3",
				Namespace: "default",
			},
		},
		{
			name: "test case 3",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			nodeAgentlist: &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node1",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node2",
							Namespace: "test",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rmd-node-agent-node3",
							Namespace: "prod",
						},
					},
				},
			},
			nodeAgentpodName: "rmd-node-agent-node3",
			namespacelist: &corev1.NamespaceList{
				Items: []corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "test",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod",
							Namespace: "prod",
						},
					},
				},
			},
			expectedNamespaceName: types.NamespacedName{
				Name:      "rmd-node-agent-node3",
				Namespace: "prod",
			},
		},
	}
	for _, tc := range tcases {
		// Create a ReconcileNode object with the scheme and fake client.
		r, err := createReconcilePodObject(tc.pod)
		if err != nil {
			t.Fatalf("error creating ReconcilePod object: (%v)", err)
		}
		for i := range tc.namespacelist.Items {
			err = r.client.Create(context.TODO(), &tc.namespacelist.Items[i])
			if err != nil {
				t.Fatalf("could not create namespace: (%v)", err)
			}
		}
		for i := range tc.nodeAgentlist.Items {
			err = r.client.Create(context.TODO(), &tc.nodeAgentlist.Items[i])
			if err != nil {
				t.Fatalf("could not create node agent pod: (%v)", err)
			}
		}
		os.Setenv("POD_NAME", tc.nodeAgentpodName)

		nodeAgent, err := r.getNodeAgentPod()
		if err != nil {
			t.Errorf("r.getNodeAgentPod: (%v)", err)
		}

		nodeAgentNamespacedName := types.NamespacedName{
			Name:      nodeAgent.GetObjectMeta().GetName(),
			Namespace: nodeAgent.GetObjectMeta().GetNamespace(),
		}

		if !reflect.DeepEqual(nodeAgentNamespacedName, tc.expectedNamespaceName) {
			t.Errorf("Failed: %v - Expected \n%v\n, got\n %v\n", tc.name, tc.expectedNamespaceName, nodeAgentNamespacedName)

		}
	}
}