/*
Copyright 2018 The Kubernetes Authors.
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

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eservice "k8s.io/kubernetes/test/e2e/framework/service"
	admissionapi "k8s.io/pod-security-admission/api"
)

var _ = Describe("[cloud-provider-aws-e2e] loadbalancer", func() {
	f := framework.NewDefaultFramework("cloud-provider-aws")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		cs clientset.Interface
		ns *v1.Namespace
	)

	BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
	})

	AfterEach(func() {
		// After each test
	})

	It("should configure the loadbalancer based on annotations", func() {
		loadBalancerCreateTimeout := e2eservice.GetServiceLoadBalancerCreationTimeout(cs)
		framework.Logf("Running tests against AWS with timeout %s", loadBalancerCreateTimeout)

		serviceName := "lbconfig-test"
		framework.Logf("namespace for load balancer conig test: %s", ns.Name)

		By("creating a TCP service " + serviceName + " with type=LoadBalancerType in namespace " + ns.Name)
		lbJig := e2eservice.NewTestJig(cs, ns.Name, serviceName)

		serviceUpdateFunc := func(svc *v1.Service) {
			annotations := make(map[string]string)
			annotations["aws-load-balancer-backend-protocol"] = "http"
			annotations["aws-load-balancer-ssl-ports"] = "https"

			svc.Annotations = annotations
			svc.Spec.Ports = []v1.ServicePort{
				{
					Name:       "http",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(80),
				},
				{
					Name:       "https",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(443),
					TargetPort: intstr.FromInt(80),
				},
			}
		}

		lbService, err := lbJig.CreateLoadBalancerService(loadBalancerCreateTimeout, serviceUpdateFunc)
		framework.ExpectNoError(err)

		By("creating a pod to be part of the TCP service " + serviceName)
		_, err = lbJig.Run(nil)
		framework.ExpectNoError(err)

		By("hitting the TCP service's LB External IP")
		svcPort := int(lbService.Spec.Ports[0].Port)
		ingressIP := e2eservice.GetIngressPoint(&lbService.Status.LoadBalancer.Ingress[0])
		framework.Logf("Load balancer's ingress IP: %s", ingressIP)

		e2eservice.TestReachableHTTP(ingressIP, svcPort, e2eservice.LoadBalancerLagTimeoutAWS)

		// Update the service to cluster IP
		By("changing TCP service back to type=ClusterIP")
		_, err = lbJig.UpdateService(func(s *v1.Service) {
			s.Spec.Type = v1.ServiceTypeClusterIP
		})
		framework.ExpectNoError(err)

		// Wait for the load balancer to be destroyed asynchronously
		_, err = lbJig.WaitForLoadBalancerDestroy(ingressIP, svcPort, loadBalancerCreateTimeout)
		framework.ExpectNoError(err)
	})

	// NLB tests
	type nlbTestCases struct {
		Name        string
		Short       string
		Annotations map[string]string
	}

	cases := []nlbTestCases{
		{
			Name:  "with security groups",
			Short: "sg",
			Annotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":                   "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-managed-security-group": "true",
			},
		},
		{
			Name:  "with security groups on workers",
			Short: "sg-wk",
			Annotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":                   "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-managed-security-group": "true",
				"service.beta.kubernetes.io/aws-load-balancer-target-node-labels":     "node-role.kubernetes.io/worker=",
			},
		},
		// TODO:  "must support hairpin connection"
	}

	testNameBase := "should configure the loadbalancer type NLB"
	svcNameBase := "test-lb-nlb"
	for _, tc := range cases {
		It(fmt.Sprintf("%s %s", testNameBase, tc.Name), func() {
			loadBalancerCreateTimeout := e2eservice.GetServiceLoadBalancerCreationTimeout(cs)
			framework.Logf("Running tests against AWS with timeout %s", loadBalancerCreateTimeout)
			suffix := tc.Short
			if len(suffix) == 0 {
				suffix = "go"
			}
			serviceName := svcNameBase + "-" + suffix
			framework.Logf("namespace for load balancer conig test: %s", ns.Name)

			By("creating a TCP service " + serviceName + " with type=LoadBalancerType in namespace " + ns.Name)
			lbJig := e2eservice.NewTestJig(cs, ns.Name, serviceName)
			lbJig.PodPort = 8080

			serviceUpdateFunc := func(svc *v1.Service) {
				annotations := make(map[string]string)
				annotations["aws-load-balancer-backend-protocol"] = "http"
				annotations["aws-load-balancer-ssl-ports"] = "https"

				// append test case annotations to the service
				for annK, annV := range tc.Annotations {
					annotations[annK] = annV
				}

				svc.Annotations = annotations
				svc.Spec.Ports = []v1.ServicePort{
					{
						Name:       "http",
						Protocol:   v1.ProtocolTCP,
						Port:       int32(80),
						TargetPort: intstr.FromInt(int(lbJig.PodPort)),
					},
					{
						Name:       "https",
						Protocol:   v1.ProtocolTCP,
						Port:       int32(443),
						TargetPort: intstr.FromInt(int(lbJig.PodPort)),
					},
				}
			}

			lbService, err := lbJig.CreateLoadBalancerService(loadBalancerCreateTimeout, serviceUpdateFunc)
			framework.ExpectNoError(err)

			By("creating a pod to be part of the TCP service " + serviceName)
			_, err = lbJig.Run(nil)
			framework.ExpectNoError(err)

			By("hitting the TCP service's LB External IP")
			svcPort := int(lbService.Spec.Ports[0].Port)
			ingressIP := e2eservice.GetIngressPoint(&lbService.Status.LoadBalancer.Ingress[0])
			framework.Logf("Load balancer's ingress IP: %s", ingressIP)

			e2eservice.TestReachableHTTP(ingressIP, svcPort, e2eservice.LoadBalancerLagTimeoutAWS)

			// Update the service to cluster IP
			By("changing TCP service back to type=ClusterIP")
			_, err = lbJig.UpdateService(func(s *v1.Service) {
				s.Spec.Type = v1.ServiceTypeClusterIP
			})
			framework.ExpectNoError(err)

			// Wait for the load balancer to be destroyed asynchronously
			_, err = lbJig.WaitForLoadBalancerDestroy(ingressIP, svcPort, loadBalancerCreateTimeout)
			framework.ExpectNoError(err)
		})
	}
})
