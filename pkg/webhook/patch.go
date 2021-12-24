/*
Copyright 2018 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"strings"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GoogleCloudPlatform/spark-on-k8s-operator/pkg/apis/sparkoperator.k8s.io/v1beta2"
	"github.com/GoogleCloudPlatform/spark-on-k8s-operator/pkg/config"
	"github.com/GoogleCloudPlatform/spark-on-k8s-operator/pkg/util"
)

const (
	maxNameLength = 63
)

// patchOperation represents a RFC6902 JSON patch operation.
type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func patchSparkPod(clientSet kubernetes.Interface, pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var patchOps []patchOperation

	if util.IsDriverPod(pod) {
		patchOps = append(patchOps, addOwnerReference(pod, app))
	}

	glog.V(3).Infof("start to patch pod of sparkApp %s in namespace", app.Name, app.Namespace)
	patchOps = append(patchOps, addVolumes(pod, app)...)
	patchOps = append(patchOps, addGeneralConfigMaps(pod, app)...)
	patchOps = append(patchOps, addSparkConfigMap(pod, app)...)
	patchOps = append(patchOps, addHadoopConfigMap(pod, app)...)
	patchOps = append(patchOps, getPrometheusConfigPatches(pod, app)...)
	patchOps = append(patchOps, addTolerations(pod, app)...)
	patchOps = append(patchOps, addSidecarContainers(pod, app)...)
	patchOps = append(patchOps, addInitContainers(pod, app)...)
	patchOps = append(patchOps, addHostNetwork(pod, app)...)
	patchOps = append(patchOps, addNodeSelectors(pod, app)...)
	patchOps = append(patchOps, addDNSConfig(pod, app)...)
	patchOps = append(patchOps, addEnvVars(pod, app)...)
	patchOps = append(patchOps, addEnvFrom(pod, app)...)
	patchOps = append(patchOps, addNodeName(pod, app)...)
	patchOps = append(patchOps, addDnsPolicy(pod, app)...)
	patchOps = append(patchOps, addAnnotations(pod, app)...)
	patchOps = append(patchOps, addRuntimeClassName(pod, app)...)
	patchOps = append(patchOps, addCustomResources(pod, app)...)
	patchOps = append(patchOps, addPVCTemplate(clientSet, pod, app)...)

	op := addSchedulerName(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	op = addPriorityClassName(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	if pod.Spec.Affinity == nil {
		op := addAffinity(pod, app)
		if op != nil {
			patchOps = append(patchOps, *op)
		}
	}

	op = addPodSecurityContext(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	op = addSecurityContext(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	op = addGPU(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	op = addTerminationGracePeriodSeconds(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	op = addPodLifeCycleConfig(pod, app)
	if op != nil {
		patchOps = append(patchOps, *op)
	}

	return patchOps
}

func addOwnerReference(pod *corev1.Pod, app *v1beta2.SparkApplication) patchOperation {
	ownerReference := util.GetOwnerReference(app)

	path := "/metadata/ownerReferences"
	var value interface{}
	if len(pod.OwnerReferences) == 0 {
		value = []metav1.OwnerReference{ownerReference}
	} else {
		path += "/-"
		value = ownerReference
	}

	return patchOperation{Op: "add", Path: path, Value: value}
}

func addVolumes(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	volumes := app.Spec.Volumes

	volumeMap := make(map[string]corev1.Volume)
	for _, v := range volumes {
		volumeMap[v.Name] = v
	}

	var volumeMounts []corev1.VolumeMount
	if util.IsDriverPod(pod) {
		volumeMounts = app.Spec.Driver.VolumeMounts
	} else if util.IsExecutorPod(pod) {
		volumeMounts = app.Spec.Executor.VolumeMounts
	}

	var ops []patchOperation
	addedVolumeMap := make(map[string]corev1.Volume)
	for _, m := range volumeMounts {
		// Skip adding localDirVolumes
		if strings.HasPrefix(m.Name, config.SparkLocalDirVolumePrefix) {
			continue
		}

		if v, ok := volumeMap[m.Name]; ok {
			if _, ok := addedVolumeMap[m.Name]; !ok {
				ops = append(ops, addVolume(pod, v))
				addedVolumeMap[m.Name] = v
			}
			vmPatchOp := addVolumeMount(pod, m)
			if vmPatchOp == nil {
				return nil
			}
			ops = append(ops, *vmPatchOp)
		}
	}
	return ops
}

func addVolume(pod *corev1.Pod, volume corev1.Volume) patchOperation {
	path := "/spec/volumes"
	var value interface{}
	if len(pod.Spec.Volumes) == 0 {
		value = []corev1.Volume{volume}
	} else {
		path += "/-"
		value = volume
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, volume)

	return patchOperation{Op: "add", Path: path, Value: value}
}

func addVolumeMount(pod *corev1.Pod, mount corev1.VolumeMount) *patchOperation {
	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add VolumeMount %s as Spark container was not found in pod %s", mount.Name, pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/volumeMounts", i)
	var value interface{}
	if len(pod.Spec.Containers[i].VolumeMounts) == 0 {
		value = []corev1.VolumeMount{mount}
	} else {
		path += "/-"
		value = mount
	}
	pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts, mount)

	return &patchOperation{Op: "add", Path: path, Value: value}
}

func addEnvVars(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var envVars []corev1.EnvVar
	var containerName string

	envVarsExist := make([]corev1.EnvVar, 0)
	for _, container := range pod.Spec.Containers {
		if container.Name == config.SparkDriverContainerName || containerName == config.SparkExecutorContainerName {
			// set default values
			envVarsExist = container.Env
		}
	}

	if util.IsDriverPod(pod) {
		envVars = app.Spec.Driver.Env
		containerName = config.SparkDriverContainerName
		envVarsDeprecated := app.Spec.Driver.EnvVars

		for k, v := range envVarsDeprecated {
			found := false
			for _, env := range envVarsExist {
				if env.Name == k {
					found = true
				}
			}
			if found == false {
				envVars = append(envVars, corev1.EnvVar{
					Name:  k,
					Value: v,
				})
			}
		}
	} else if util.IsExecutorPod(pod) {
		envVars = app.Spec.Executor.Env
		containerName = config.SparkExecutorContainerName
		envVarsDeprecated := app.Spec.Executor.EnvVars

		for k, v := range envVarsDeprecated {
			found := false
			for _, env := range envVarsExist {
				if env.Name == k {
					found = true
				}
			}
			if found == false {
				envVars = append(envVars, corev1.EnvVar{
					Name:  k,
					Value: v,
				})
			}
		}
	}

	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add EnvVars as Spark container was not found in pod %s", pod.Name)
		return nil
	}
	basePath := fmt.Sprintf("/spec/containers/%d/env", i)

	var value interface{}
	var patchOps []patchOperation

	first := false
	if len(pod.Spec.Containers[i].Env) == 0 {
		first = true
	}

	for _, envVar := range envVars {
		path := basePath
		if first {
			value = []corev1.EnvVar{envVar}
			first = false
		} else {
			path += "/-"
			value = envVar
		}
		patchOps = append(patchOps, patchOperation{Op: "add", Path: path, Value: value})
	}
	return patchOps
}

func addEnvFrom(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var envFrom []corev1.EnvFromSource
	if util.IsDriverPod(pod) {
		envFrom = app.Spec.Driver.EnvFrom
	} else if util.IsExecutorPod(pod) {
		envFrom = app.Spec.Executor.EnvFrom
	}

	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add EnvFrom as Spark container was not found in pod %s", pod.Name)
		return nil
	}
	basePath := fmt.Sprintf("/spec/containers/%d/envFrom", i)

	var value interface{}
	var patchOps []patchOperation

	first := false
	if len(pod.Spec.Containers[i].EnvFrom) == 0 {
		first = true
	}

	for _, ef := range envFrom {
		path := basePath
		if first {
			value = []corev1.EnvFromSource{ef}
			first = false
		} else {
			path += "/-"
			value = ef
		}
		patchOps = append(patchOps, patchOperation{Op: "add", Path: path, Value: value})
	}
	return patchOps
}

func addEnvironmentVariable(pod *corev1.Pod, envName, envValue string) *patchOperation {
	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add environment variable %s as Spark container was not found in pod %s", envName, pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/env", i)
	var value interface{}
	if len(pod.Spec.Containers[i].Env) == 0 {
		value = []corev1.EnvVar{{Name: envName, Value: envValue}}
	} else {
		path += "/-"
		value = corev1.EnvVar{Name: envName, Value: envValue}
	}

	return &patchOperation{Op: "add", Path: path, Value: value}
}

func addSparkConfigMap(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var patchOps []patchOperation
	sparkConfigMapName := app.Spec.SparkConfigMap
	if sparkConfigMapName != nil {
		patchOps = append(patchOps, addConfigMapVolume(pod, *sparkConfigMapName, config.SparkConfigMapVolumeName))
		vmPatchOp := addConfigMapVolumeMount(pod, config.SparkConfigMapVolumeName, config.DefaultSparkConfDir)
		if vmPatchOp == nil {
			return nil
		}
		patchOps = append(patchOps, *vmPatchOp)
		envPatchOp := addEnvironmentVariable(pod, config.SparkConfDirEnvVar, config.DefaultSparkConfDir)
		if envPatchOp == nil {
			return nil
		}
		patchOps = append(patchOps, *envPatchOp)
	}
	return patchOps
}

func addHadoopConfigMap(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var patchOps []patchOperation
	hadoopConfigMapName := app.Spec.HadoopConfigMap
	if hadoopConfigMapName != nil {
		patchOps = append(patchOps, addConfigMapVolume(pod, *hadoopConfigMapName, config.HadoopConfigMapVolumeName))
		vmPatchOp := addConfigMapVolumeMount(pod, config.HadoopConfigMapVolumeName, config.DefaultHadoopConfDir)
		if vmPatchOp == nil {
			return nil
		}
		patchOps = append(patchOps, *vmPatchOp)
		envPatchOp := addEnvironmentVariable(pod, config.HadoopConfDirEnvVar, config.DefaultHadoopConfDir)
		if envPatchOp == nil {
			return nil
		}
		patchOps = append(patchOps, *envPatchOp)
	}
	return patchOps
}

func addGeneralConfigMaps(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var configMaps []v1beta2.NamePath
	if util.IsDriverPod(pod) {
		configMaps = app.Spec.Driver.ConfigMaps
	} else if util.IsExecutorPod(pod) {
		configMaps = app.Spec.Executor.ConfigMaps
	}

	var patchOps []patchOperation
	for _, namePath := range configMaps {
		volumeName := namePath.Name + "-vol"
		if len(volumeName) > maxNameLength {
			volumeName = volumeName[0:maxNameLength]
			glog.V(2).Infof("ConfigMap volume name is too long. Truncating to length %d. Result: %s.", maxNameLength, volumeName)
		}
		patchOps = append(patchOps, addConfigMapVolume(pod, namePath.Name, volumeName))
		vmPatchOp := addConfigMapVolumeMount(pod, volumeName, namePath.Path)
		if vmPatchOp == nil {
			return nil
		}
		patchOps = append(patchOps, *vmPatchOp)
	}
	return patchOps
}

func getPrometheusConfigPatches(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	// Skip if Prometheus Monitoring is not enabled or an in-container ConfigFile is used,
	// in which cases a Prometheus ConfigMap won't be created.
	if !app.PrometheusMonitoringEnabled() || (app.HasMetricsPropertiesFile() && app.HasPrometheusConfigFile()) {
		return nil
	}

	if util.IsDriverPod(pod) && !app.ExposeDriverMetrics() {
		return nil
	}
	if util.IsExecutorPod(pod) && !app.ExposeExecutorMetrics() {
		return nil
	}

	var patchOps []patchOperation
	name := config.GetPrometheusConfigMapName(app)
	volumeName := name + "-vol"
	mountPath := config.PrometheusConfigMapMountPath
	port := config.DefaultPrometheusJavaAgentPort
	if app.Spec.Monitoring.Prometheus.Port != nil {
		port = *app.Spec.Monitoring.Prometheus.Port
	}
	protocol := config.DefaultPrometheusPortProtocol
	portName := config.DefaultPrometheusPortName
	if app.Spec.Monitoring.Prometheus.PortName != nil {
		portName = *app.Spec.Monitoring.Prometheus.PortName
	}

	patchOps = append(patchOps, addConfigMapVolume(pod, name, volumeName))
	vmPatchOp := addConfigMapVolumeMount(pod, volumeName, mountPath)
	if vmPatchOp == nil {
		glog.Warningf("could not mount volume %s in path %s", volumeName, mountPath)
		return nil
	}
	patchOps = append(patchOps, *vmPatchOp)
	portPatchOp := addContainerPort(pod, port, protocol, portName)
	if portPatchOp == nil {
		glog.Warningf("could not expose port %d to scrape metrics outside the pod", port)
		return nil
	}
	patchOps = append(patchOps, *portPatchOp)

	return patchOps
}

func addContainerPort(pod *corev1.Pod, port int32, protocol string, portName string) *patchOperation {
	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add containerPort %d as Spark container was not found in pod %s", port, pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/ports", i)
	containerPort := corev1.ContainerPort{
		Name:          portName,
		ContainerPort: port,
		Protocol:      corev1.Protocol(protocol),
	}
	var value interface{}
	if len(pod.Spec.Containers[i].Ports) == 0 {
		value = []corev1.ContainerPort{containerPort}
	} else {
		path += "/-"
		value = containerPort
	}
	pod.Spec.Containers[i].Ports = append(pod.Spec.Containers[i].Ports, containerPort)
	return &patchOperation{Op: "add", Path: path, Value: value}
}

func addConfigMapVolume(pod *corev1.Pod, configMapName string, configMapVolumeName string) patchOperation {
	volume := corev1.Volume{
		Name: configMapVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
			},
		},
	}
	return addVolume(pod, volume)
}

func addConfigMapVolumeMount(pod *corev1.Pod, configMapVolumeName string, mountPath string) *patchOperation {
	mount := corev1.VolumeMount{
		Name:      configMapVolumeName,
		ReadOnly:  true,
		MountPath: mountPath,
	}
	return addVolumeMount(pod, mount)
}

func addAffinity(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var affinity *corev1.Affinity
	if util.IsDriverPod(pod) {
		affinity = app.Spec.Driver.Affinity
	} else if util.IsExecutorPod(pod) {
		affinity = app.Spec.Executor.Affinity
	}

	if affinity == nil {
		return nil
	}
	return &patchOperation{Op: "add", Path: "/spec/affinity", Value: *affinity}
}

func addTolerations(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var tolerations []corev1.Toleration
	if util.IsDriverPod(pod) {
		tolerations = app.Spec.Driver.Tolerations
	} else if util.IsExecutorPod(pod) {
		tolerations = app.Spec.Executor.Tolerations
	}

	first := false
	if len(pod.Spec.Tolerations) == 0 {
		first = true
	}

	var ops []patchOperation
	for _, v := range tolerations {
		ops = append(ops, addToleration(pod, v, first))
		if first {
			first = false
		}
	}
	return ops
}

func addNodeSelectors(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var nodeSelector map[string]string
	if util.IsDriverPod(pod) {
		nodeSelector = app.Spec.Driver.NodeSelector
	} else if util.IsExecutorPod(pod) {
		nodeSelector = app.Spec.Executor.NodeSelector
	}

	var ops []patchOperation
	if len(nodeSelector) > 0 {
		ops = append(ops, patchOperation{Op: "add", Path: "/spec/nodeSelector", Value: nodeSelector})
	}
	return ops
}

func addDNSConfig(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var dnsConfig *corev1.PodDNSConfig

	if util.IsDriverPod(pod) {
		dnsConfig = app.Spec.Driver.DNSConfig
	} else if util.IsExecutorPod(pod) {
		dnsConfig = app.Spec.Executor.DNSConfig
	}

	var ops []patchOperation
	if dnsConfig != nil {
		ops = append(ops, patchOperation{Op: "add", Path: "/spec/dnsConfig", Value: dnsConfig})
	}
	return ops
}

func addSchedulerName(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var schedulerName *string

	//NOTE: Preferred to use `BatchScheduler` if application spec has it configured.
	if app.Spec.BatchScheduler != nil {
		schedulerName = app.Spec.BatchScheduler
	} else if util.IsDriverPod(pod) {
		schedulerName = app.Spec.Driver.SchedulerName
	} else if util.IsExecutorPod(pod) {
		schedulerName = app.Spec.Executor.SchedulerName
	}
	if schedulerName == nil || *schedulerName == "" {
		return nil
	}
	return &patchOperation{Op: "add", Path: "/spec/schedulerName", Value: *schedulerName}
}

func addPriorityClassName(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var priorityClassName *string

	if app.Spec.BatchSchedulerOptions != nil {
		priorityClassName = app.Spec.BatchSchedulerOptions.PriorityClassName
	}

	if priorityClassName == nil || *priorityClassName == "" {
		return nil
	}
	return &patchOperation{Op: "add", Path: "/spec/priorityClassName", Value: *priorityClassName}
}

func addToleration(pod *corev1.Pod, toleration corev1.Toleration, first bool) patchOperation {
	path := "/spec/tolerations"
	var value interface{}
	if first {
		value = []corev1.Toleration{toleration}
	} else {
		path += "/-"
		value = toleration
	}

	return patchOperation{Op: "add", Path: path, Value: value}
}

func addPodSecurityContext(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var secContext *corev1.PodSecurityContext
	if util.IsDriverPod(pod) {
		secContext = app.Spec.Driver.PodSecurityContext
	} else if util.IsExecutorPod(pod) {
		secContext = app.Spec.Executor.PodSecurityContext
	}

	if secContext == nil {
		return nil
	}
	return &patchOperation{Op: "add", Path: "/spec/securityContext", Value: *secContext}
}

func addSecurityContext(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var secContext *corev1.SecurityContext
	if util.IsDriverPod(pod) {
		secContext = app.Spec.Driver.SecurityContext
	} else if util.IsExecutorPod(pod) {
		secContext = app.Spec.Executor.SecurityContext
	}

	if secContext == nil {
		return nil
	}

	i := 0
	// Find the driver/executor container in the pod.
	for ; i < len(pod.Spec.Containers); i++ {
		if pod.Spec.Containers[i].Name == config.SparkDriverContainerName || pod.Spec.Containers[i].Name == config.SparkExecutorContainerName {
			break
		}
	}
	if i == len(pod.Spec.Containers) {
		glog.Warningf("Spark driver/executor container not found in pod %s", pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/securityContext", i)
	return &patchOperation{Op: "add", Path: path, Value: *secContext}
}

func addSidecarContainers(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var sidecars []corev1.Container
	if util.IsDriverPod(pod) {
		sidecars = app.Spec.Driver.Sidecars
	} else if util.IsExecutorPod(pod) {
		sidecars = app.Spec.Executor.Sidecars
	}

	var ops []patchOperation
	for _, c := range sidecars {
		sd := c
		if !hasContainer(pod, &sd) {
			ops = append(ops, patchOperation{Op: "add", Path: "/spec/containers/-", Value: &sd})
		}
	}
	return ops
}

func addInitContainers(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var initContainers []corev1.Container
	if util.IsDriverPod(pod) {
		initContainers = app.Spec.Driver.InitContainers
	} else if util.IsExecutorPod(pod) {
		initContainers = app.Spec.Executor.InitContainers
	}

	first := false
	if len(pod.Spec.InitContainers) == 0 {
		first = true
	}

	var ops []patchOperation
	for _, c := range initContainers {
		sd := c
		if first {
			first = false
			value := []corev1.Container{sd}
			ops = append(ops, patchOperation{Op: "add", Path: "/spec/initContainers", Value: value})
		} else if !hasInitContainer(pod, &sd) {
			ops = append(ops, patchOperation{Op: "add", Path: "/spec/initContainers/-", Value: &sd})
		}

	}
	return ops
}

func addGPU(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var gpu *v1beta2.GPUSpec
	if util.IsDriverPod(pod) {
		gpu = app.Spec.Driver.GPU
	}
	if util.IsExecutorPod(pod) {
		gpu = app.Spec.Executor.GPU
	}
	if gpu == nil {
		return nil
	}
	if gpu.Name == "" {
		glog.V(2).Infof("Please specify GPU resource name, such as: nvidia.com/gpu, amd.com/gpu etc. Current gpu spec: %+v", gpu)
		return nil
	}
	if gpu.Quantity <= 0 {
		glog.V(2).Infof("GPU Quantity must be positive. Current gpu spec: %+v", gpu)
		return nil
	}

	i := findContainer(pod)
	if i < 0 {
		glog.Warningf("not able to add GPU as Spark container was not found in pod %s", pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/resources/limits", i)
	var value interface{}
	if len(pod.Spec.Containers[i].Resources.Limits) == 0 {
		value = corev1.ResourceList{
			corev1.ResourceName(gpu.Name): *resource.NewQuantity(gpu.Quantity, resource.DecimalSI),
		}
	} else {
		encoder := strings.NewReplacer("~", "~0", "/", "~1")
		path += "/" + encoder.Replace(gpu.Name)
		value = *resource.NewQuantity(gpu.Quantity, resource.DecimalSI)
	}
	return &patchOperation{Op: "add", Path: path, Value: value}
}

func addHostNetwork(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var hostNetwork *bool
	if util.IsDriverPod(pod) {
		hostNetwork = app.Spec.Driver.HostNetwork
	}
	if util.IsExecutorPod(pod) {
		hostNetwork = app.Spec.Executor.HostNetwork
	}

	if hostNetwork == nil || *hostNetwork == false {
		return nil
	}
	var ops []patchOperation
	ops = append(ops, patchOperation{Op: "add", Path: "/spec/hostNetwork", Value: true})
	// For Pods with hostNetwork, explicitly set its DNS policy  to “ClusterFirstWithHostNet”
	// Detail: https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#pod-s-dns-policy
	ops = append(ops, patchOperation{Op: "add", Path: "/spec/dnsPolicy", Value: corev1.DNSClusterFirstWithHostNet})
	return ops
}

func addNodeName(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var nodeName *string
	if util.IsDriverPod(pod) {
		nodeName = app.Spec.Driver.NodeName
	}
	if util.IsExecutorPod(pod) {
		nodeName = app.Spec.Executor.NodeName
	}

	if nodeName == nil {
		return nil
	}

	var ops []patchOperation
	ops = append(ops, patchOperation{Op: "add", Path: "/spec/nodeName", Value: nodeName})
	// For Pods with hostNetwork, explicitly set its DNS policy  to “ClusterFirstWithHostNet”
	// Detail: https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#pod-s-dns-policy
	return ops
}

func addDnsPolicy(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var ops []patchOperation
	var dnsPolicy corev1.DNSPolicy
	if util.IsDriverPod(pod) && !util.IsHostNetwork(pod) {
		dnsPolicy = app.Spec.Driver.DNSPolicy
	}

	if util.IsExecutorPod(pod) && !util.IsHostNetwork(pod) {
		dnsPolicy = app.Spec.Executor.DNSPolicy
	}

	if dnsPolicy != "" {
		ops = append(ops, patchOperation{Op: "add", Path: "/spec/dnsPolicy", Value: dnsPolicy})
	}

	return ops
}

func addAnnotations(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var annotations map[string]string
	if util.IsDriverPod(pod) {
		annotations = app.Spec.Driver.Annotations
	}
	if util.IsExecutorPod(pod) {
		annotations = app.Spec.Executor.Annotations
	}

	if annotations == nil {
		return nil
	}

	var ops []patchOperation
	ops = append(ops, patchOperation{Op: "add", Path: "/metadata/annotations", Value: annotations})
	// For Pods with hostNetwork, explicitly set its DNS policy  to “ClusterFirstWithHostNet”
	// Detail: https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#pod-s-dns-policy
	return ops
}

// add runtimeClassName to spark pod
func addRuntimeClassName(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var runtimeClassName string
	if util.IsDriverPod(pod) {
		runtimeClassName = app.Spec.Driver.RuntimeClassName
	}
	if util.IsExecutorPod(pod) {
		runtimeClassName = app.Spec.Executor.RuntimeClassName
	}

	if runtimeClassName == "" {
		return nil
	}

	var ops []patchOperation
	ops = append(ops, patchOperation{Op: "add", Path: "/spec/runtimeClassName", Value: runtimeClassName})
	return ops
}

func addCustomResources(pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	var resource corev1.ResourceRequirements
	ops := make([]patchOperation, 0)
	if util.IsDriverPod(pod) {
		resource = app.Spec.Driver.CustomResources
	}
	if util.IsExecutorPod(pod) {
		resource = app.Spec.Executor.CustomResources
	}

	found := false

	i := 0
	// Find the driver or executor container in the pod.
	for ; i < len(pod.Spec.Containers); i++ {
		if pod.Spec.Containers[i].Name == config.SparkDriverContainerName ||
			pod.Spec.Containers[i].Name == config.SparkExecutorContainerName ||
			pod.Spec.Containers[i].Name == config.Spark3DefaultExecutorContainerName {
			found = true
			break
		}
	}

	if found == false {
		glog.V(5).Infof("Failed to find container index %v", pod.Spec.Containers)
		return ops
	}

	requestsPath := fmt.Sprintf("/spec/containers/%d/resources/requests", i)
	limitsPath := fmt.Sprintf("/spec/containers/%d/resources/limits", i)
	encoder := strings.NewReplacer("~", "~0", "/", "~1")

	if len(resource.Requests) != 0 {
		if len(pod.Spec.Containers[i].Resources.Requests) == 0 {
			for k, v := range resource.Requests {
				ops = append(ops, patchOperation{Op: "add", Path: requestsPath, Value: corev1.ResourceList{
					corev1.ResourceName(encoder.Replace(string(k))): v,
				}})
			}
		} else {
			for k, v := range resource.Requests {
				ops = append(ops, patchOperation{Op: "add", Path: fmt.Sprintf("%s/%s", requestsPath, encoder.Replace(string(k))), Value: v})
			}
		}
	}
	if len(resource.Limits) != 0 {
		if len(pod.Spec.Containers[i].Resources.Limits) == 0 {
			for k, v := range resource.Limits {
				ops = append(ops, patchOperation{Op: "add", Path: limitsPath, Value: corev1.ResourceList{
					corev1.ResourceName(encoder.Replace(string(k))): v,
				}})
			}
		} else {
			for k, v := range resource.Limits {
				ops = append(ops, patchOperation{Op: "add", Path: fmt.Sprintf("%s/%s", limitsPath, encoder.Replace(string(k))), Value: v})
			}
		}
	}
	return ops
}

func hasContainer(pod *corev1.Pod, container *corev1.Container) bool {
	for _, c := range pod.Spec.Containers {
		if container.Name == c.Name && container.Image == c.Image {
			return true
		}
	}
	return false
}

func hasInitContainer(pod *corev1.Pod, container *corev1.Container) bool {
	for _, c := range pod.Spec.InitContainers {
		if container.Name == c.Name && container.Image == c.Image {
			return true
		}
	}
	return false
}

func addTerminationGracePeriodSeconds(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	path := "/spec/terminationGracePeriodSeconds"
	var gracePeriodSeconds *int64

	if util.IsDriverPod(pod) {
		gracePeriodSeconds = app.Spec.Driver.TerminationGracePeriodSeconds
	} else if util.IsExecutorPod(pod) {
		gracePeriodSeconds = app.Spec.Executor.TerminationGracePeriodSeconds
	}
	if gracePeriodSeconds == nil {
		return nil
	}
	return &patchOperation{Op: "add", Path: path, Value: *gracePeriodSeconds}
}

func addPodLifeCycleConfig(pod *corev1.Pod, app *v1beta2.SparkApplication) *patchOperation {
	var lifeCycle *corev1.Lifecycle
	if util.IsDriverPod(pod) {
		lifeCycle = app.Spec.Driver.Lifecycle
	}
	if lifeCycle == nil {
		return nil
	}
	i := 0
	// Find the driver container in the pod.
	for ; i < len(pod.Spec.Containers); i++ {
		if pod.Spec.Containers[i].Name == config.SparkDriverContainerName {
			break
		}
	}
	if i == len(pod.Spec.Containers) {
		glog.Warningf("Spark driver container not found in pod %s", pod.Name)
		return nil
	}

	path := fmt.Sprintf("/spec/containers/%d/lifecycle", i)
	return &patchOperation{Op: "add", Path: path, Value: *lifeCycle}
}
func findContainer(pod *corev1.Pod) int {
	var candidateContainerNames []string
	if util.IsDriverPod(pod) {
		candidateContainerNames = append(candidateContainerNames, config.SparkDriverContainerName)
	} else if util.IsExecutorPod(pod) {
		// Spark 3.x changed the default executor container name so we need to include both.
		candidateContainerNames = append(candidateContainerNames, config.SparkExecutorContainerName, config.Spark3DefaultExecutorContainerName)
	}
	if len(candidateContainerNames) == 0 {
		return -1
	}
	for i := 0; i < len(pod.Spec.Containers); i++ {
		for _, name := range candidateContainerNames {
			if pod.Spec.Containers[i].Name == name {
				return i
			}
		}
	}
	return -1
}

func addPVCTemplate(clientSet kubernetes.Interface, pod *corev1.Pod, app *v1beta2.SparkApplication) []patchOperation {
	namespace := app.Namespace
	podName := pod.Name
	// Find index of pod
	index := splitIndexFromPodName(podName)
	// Current Pod volumes
	volumes := pod.Spec.Volumes

	ops := make([]patchOperation, 0)

	// Get this pod volumeMounts spec
	var volumeMounts []corev1.VolumeMount
	if util.IsDriverPod(pod) {
		volumeMounts = app.Spec.Driver.VolumeMounts
	} else if util.IsExecutorPod(pod) {
		volumeMounts = app.Spec.Executor.VolumeMounts
	}

	// Get vpc template definition
	volumeClaimTemplates := app.Spec.VolumeClaimTemplates
	if len(volumeClaimTemplates) != 0 {
		for _, vct := range volumeClaimTemplates {
			// storage name to match volume
			storageName := vct.Name
			prefix := app.Name
			suffix := fmt.Sprintf("-%s-%s", vct.Name, index)

			if len(prefix) > (maxNameLength - len(suffix)) {
				prefix = prefix[0 : maxNameLength-len(suffix)]
			}
			// unique index clain name
			clainName := fmt.Sprintf("%s%s", prefix, suffix)
			vct.Name = clainName
			vct.Namespace = namespace
			// set owner references to handle deletions
			vct.SetOwnerReferences([]metav1.OwnerReference{
				util.GetOwnerReference(app),
			})

			glog.V(5).Infof("Try to find PersistentVolumeClaims to check pod pvc %s", clainName)
			// get or create unique pvc
			_, err := clientSet.CoreV1().PersistentVolumeClaims(namespace).Get(clainName, metav1.GetOptions{})
			if err != nil {
				glog.V(5).Infof("Failed to find PersistentVolumeClaims %s because of %v.", clainName, err)
			}
			if err != nil && errors.IsNotFound(err) {
				glog.V(5).Infof("Failed to find PersistentVolumeClaims %s and try to create one.", clainName)
				_, err := clientSet.CoreV1().PersistentVolumeClaims(namespace).Create(&vct)
				if err != nil {
					glog.Errorf("Failed to create pvc %s because of %v", clainName, err)
					continue
				}
			}

			found := false
			for _, v := range volumes {
				// check current volumes to find clain
				if v.Name == storageName {
					found = true
				}
			}

			if !found {
				for _, m := range volumeMounts {
					// check volumes to match storage
					if m.Name == storageName {
						v := corev1.Volume{
							Name: storageName,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: clainName,
									// TODO: Use source definition to set this value when we have one.
									ReadOnly: false,
								},
							}}
						ops = append(ops, addVolume(pod, v))
						vmPatchOp := addVolumeMount(pod, m)
						if vmPatchOp == nil {
							continue
						}
						ops = append(ops, *vmPatchOp)
					}
				}
			}
		}
	}
	glog.V(5).Infof("Add all PersistentVolumeClaims patch %v", ops)
	return ops
}

func splitIndexFromPodName(name string) string {
	arr := strings.Split(name, "-")
	if len(arr) != 1 {
		return arr[len(arr)-1]
	}
	return ""
}
