/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	coccyxv1alpha1 "coccyx-rtsp-stream-operator/api/v1alpha1"
)

// RtspStreamReconciler reconciles a RtspStream object
type RtspStreamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=coccyx.dev,resources=rtspstreams,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=coccyx.dev,resources=rtspstreams/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=coccyx.dev,resources=rtspstreams/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete

func (r *RtspStreamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the RtspStream instance
	rtspStream := &coccyxv1alpha1.RtspStream{}
	err := r.Get(ctx, req.NamespacedName, rtspStream)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("RtspStream resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get RtspStream")
		return ctrl.Result{}, err
	}

	// Check if the pod already exists, if not create a new one
	found := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: podName(rtspStream), Namespace: rtspStream.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new pod
		pod := r.podForRtspStream(rtspStream)
		log.Info("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
		err = r.Create(ctx, pod)
		if err != nil {
			log.Error(err, "Failed to create new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
			return ctrl.Result{}, err
		}
		// Pod created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func podName(rtspStream *coccyxv1alpha1.RtspStream) string {
	return fmt.Sprintf("rtsp-reader-%s", rtspStream.Spec.ID)
}

// podForRtspStream returns a coccyx-rtsp-frame-reader Pod object
func (r *RtspStreamReconciler) podForRtspStream(rtspStream *coccyxv1alpha1.RtspStream) *corev1.Pod {
	ls := labelsForRtspStream(rtspStream)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName(rtspStream),
			Namespace: rtspStream.Namespace,
			Labels:    ls,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Image:           "coccyx-rtsp-frame-reader:latest",
				ImagePullPolicy: corev1.PullNever,
				Name:            "rtsp-frame-reader",
				Env: []corev1.EnvVar{
					{
						Name:  "RTSP_URL",
						Value: rtspStream.Spec.URL,
					},
					{
						Name:  "STREAM_ID",
						Value: rtspStream.Spec.ID,
					},
					{
						Name:  "NATS_URL",
						Value: "nats://nats:4222",
					},
					{
						Name:  "LOG_FILENAME",
						Value: "rtsp_frame_reader.log",
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "http",
						ContainerPort: 8080,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"memory": resource.MustParse("2Gi"),
						"cpu":    resource.MustParse("1000m"),
					},
					Limits: corev1.ResourceList{
						"memory": resource.MustParse("8Gi"),
						"cpu":    resource.MustParse("4000m"),
					},
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/health",
							Port: intstr.FromInt(8080),
						},
					},
					InitialDelaySeconds: 30,
					PeriodSeconds:       30,
					TimeoutSeconds:      5,
					FailureThreshold:    3,
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/ready",
							Port: intstr.FromInt(8080),
						},
					},
					InitialDelaySeconds: 10,
					PeriodSeconds:       10,
					TimeoutSeconds:      5,
					FailureThreshold:    3,
				},
			}},
		},
	}
	// Set RtspStream instance as the owner and controller
	ctrl.SetControllerReference(rtspStream, pod, r.Scheme)
	return pod
}

// labelsForRtspStream returns the labels for selecting the resources
// belonging to the given rtspStream CR name.
func labelsForRtspStream(rtspStream *coccyxv1alpha1.RtspStream) map[string]string {
	return map[string]string{"app": "rtsp-frame-reader", "stream-id": rtspStream.Spec.ID}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RtspStreamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&coccyxv1alpha1.RtspStream{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
