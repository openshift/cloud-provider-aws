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
	"context"
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eservice "k8s.io/kubernetes/test/e2e/framework/service"
	imageutils "k8s.io/kubernetes/test/utils/image"
	admissionapi "k8s.io/pod-security-admission/api"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

// loadbalancer tests
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

	type loadBalancerTestCases struct {
		Name               string
		ResourceSuffix     string
		Annotations        map[string]string
		PostRunValidations func(cfg *configServiceLB, svc *v1.Service)
	}

	cases := []loadBalancerTestCases{
		{
			Name:           "should configure the loadbalancer based on annotations",
			ResourceSuffix: "",
			Annotations:    map[string]string{},
		},
		{
			Name:           "NLB should configure the loadbalancer based on annotations",
			ResourceSuffix: "nlb",
			Annotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
			},
		},
		{
			Name:           "NLB should configure the loadbalancer with target-node-labels",
			ResourceSuffix: "sg-wk",
			Annotations: map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":               "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-target-node-labels": "node-role.kubernetes.io/worker=",
			},
			PostRunValidations: func(cfg *configServiceLB, svc *v1.Service) {
				j := cfg.LBJig
				nodeList, err := j.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{
					LabelSelector: "node-role.kubernetes.io/worker=",
				})
				framework.ExpectNoError(err, "failed to list worker nodes")

				workerNodes := len(nodeList.Items)
				framework.Logf("Found %d worker nodes", workerNodes)

				// Validate in the TG if the node count matches with expected target-node-labels selector.
				lbDNS := svc.Status.LoadBalancer.Ingress[0].Hostname
				framework.ExpectNoError(getLBTargetCount(context.TODO(), lbDNS, workerNodes), "AWS LB target count validation failed")
			},
		},
	}

	serviceNameBase := "lbconfig-test"
	for _, tc := range cases {
		It(tc.Name, func() {
			loadBalancerCreateTimeout := e2eservice.GetServiceLoadBalancerCreationTimeout(cs)
			framework.Logf("Running tests against AWS with timeout %s", loadBalancerCreateTimeout)

			// Create Configuration
			serviceName := serviceNameBase
			if len(tc.ResourceSuffix) > 0 {
				serviceName = fmt.Sprintf("%s-%s", serviceName, tc.ResourceSuffix)
			}
			framework.Logf("namespace for load balancer conig test: %s", ns.Name)

			By("creating a TCP service " + serviceName + " with type=LoadBalancerType in namespace " + ns.Name)
			lbConfig := newConfigServiceLB()
			lbConfig.LBJig = e2eservice.NewTestJig(cs, ns.Name, serviceName)
			lbServiceConfig := lbConfig.buildService(tc.Annotations)

			// Create Load Balancer
			ginkgo.By("creating loadbalancer for service " + lbServiceConfig.Namespace + "/" + lbServiceConfig.Name)
			_, err := lbConfig.LBJig.Client.CoreV1().Services(lbConfig.LBJig.Namespace).Create(context.TODO(), lbServiceConfig, metav1.CreateOptions{})
			framework.ExpectNoError(fmt.Errorf("failed to create LoadBalancer Service %q: %v", lbServiceConfig.Name, err))

			ginkgo.By("waiting for loadbalancer for service " + lbServiceConfig.Namespace + "/" + lbServiceConfig.Name)
			lbService, err := lbConfig.LBJig.WaitForLoadBalancer(loadBalancerCreateTimeout)
			framework.ExpectNoError(err)

			// Run Workloads
			By("creating a pod to be part of the TCP service " + serviceName)
			_, err = lbConfig.LBJig.Run(lbConfig.buildReplicationController())
			framework.ExpectNoError(err)

			if tc.PostRunValidations != nil {
				By("running post run validations")
				tc.PostRunValidations(lbConfig, lbService)
			}

			// Test the Service Endpoint
			By("hitting the TCP service's LB External IP")
			svcPort := int(lbService.Spec.Ports[0].Port)
			ingressIP := e2eservice.GetIngressPoint(&lbService.Status.LoadBalancer.Ingress[0])
			framework.Logf("Load balancer's ingress IP: %s", ingressIP)

			e2eservice.TestReachableHTTP(ingressIP, svcPort, e2eservice.LoadBalancerLagTimeoutAWS)

			// Update the service to cluster IP
			By("changing TCP service back to type=ClusterIP")
			_, err = lbConfig.LBJig.UpdateService(func(s *v1.Service) {
				s.Spec.Type = v1.ServiceTypeClusterIP
			})
			framework.ExpectNoError(err)

			// Wait for the load balancer to be destroyed asynchronously
			_, err = lbConfig.LBJig.WaitForLoadBalancerDestroy(ingressIP, svcPort, loadBalancerCreateTimeout)
			framework.ExpectNoError(err)
		})
	}
})

// configServiceLB hold loadbalancer test configurations
type configServiceLB struct {
	PodPort            uint16
	PodProtocol        v1.Protocol
	DefaultAnnotations map[string]string

	LBJig *e2eservice.TestJig
}

func newConfigServiceLB() *configServiceLB {
	return &configServiceLB{
		PodPort:     8080,
		PodProtocol: v1.ProtocolTCP,
		DefaultAnnotations: map[string]string{
			"aws-load-balancer-backend-protocol": "http",
			"aws-load-balancer-ssl-ports":        "https",
		},
	}
}

func (s *configServiceLB) buildService(extraAnnotations map[string]string) *v1.Service {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   s.LBJig.Namespace,
			Name:        s.LBJig.Name,
			Labels:      s.LBJig.Labels,
			Annotations: make(map[string]string, len(s.DefaultAnnotations)+len(extraAnnotations)),
		},
		Spec: v1.ServiceSpec{
			Type:            v1.ServiceTypeLoadBalancer,
			SessionAffinity: v1.ServiceAffinityNone,
			Selector:        s.LBJig.Labels,
			Ports: []v1.ServicePort{
				{
					Name:       "http",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(int(s.PodPort)),
				},
				{
					Name:       "https",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(443),
					TargetPort: intstr.FromInt(int(s.PodPort)),
				},
			},
		},
	}

	// add default annotations - can be overriden by extra annotations
	for aK, aV := range s.DefaultAnnotations {
		svc.Annotations[aK] = aV
	}

	// append test case annotations to the service
	for aK, aV := range extraAnnotations {
		svc.Annotations[aK] = aV
	}

	return svc
}

// buildReplicationController creates a replication controller wrapper for the test framework.
// buildReplicationController is basaed on newRCTemplate() from the test, which not provide
// customization to bind in non-privileged ports.
// TODO(mtulio): v1.33+[2] moved from RC to Deployments on tests, we must do the same to use Run()
// when the test framework is updated.
// [1] https://github.com/kubernetes/kubernetes/blob/89d95c9713a8fd189e8ad555120838b3c4f888d1/test/e2e/framework/service/jig.go#L636
// [2] https://github.com/kubernetes/kubernetes/issues/119021
func (s *configServiceLB) buildReplicationController() func(rc *v1.ReplicationController) {
	return func(rc *v1.ReplicationController) {
		var replicas int32 = 1
		var grace int64 = 3 // so we don't race with kube-proxy when scaling up/down
		rc.ObjectMeta = metav1.ObjectMeta{
			Namespace: s.LBJig.Namespace,
			Name:      s.LBJig.Name,
			Labels:    s.LBJig.Labels,
		}
		rc.Spec = v1.ReplicationControllerSpec{
			Replicas: &replicas,
			Selector: s.LBJig.Labels,
			Template: &v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: s.LBJig.Labels,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "netexec",
							Image: imageutils.GetE2EImage(imageutils.Agnhost),
							Args: []string{
								"netexec",
								fmt.Sprintf("--http-port=%d", s.PodPort),
								fmt.Sprintf("--udp-port=%d", s.PodPort),
							},
							ReadinessProbe: &v1.Probe{
								PeriodSeconds: 3,
								ProbeHandler: v1.ProbeHandler{
									HTTPGet: &v1.HTTPGetAction{
										Port: intstr.FromInt(int(s.PodPort)),
										Path: "/hostName",
									},
								},
							},
						},
					},
					TerminationGracePeriodSeconds: &grace,
				},
			},
		}
	}
}

// getLBTargetCount verifies the number of registered targets for a given LBv2 DNS name matches the expected count.
// The steps includes:
// 1. Get Load Balancer ARN from DNS name extracted from service Status.LoadBalancer.Ingress[0].Hostname
// 2. List listeners for the load balancer
// 3. Get target groups attached to listeners
// 4. Count registered targets in target groups
// 5. Verify count matches number of worker nodes
func getLBTargetCount(ctx context.Context, lbDNSName string, expectedTargets int) error {
	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("unable to load AWS config: %v", err)
	}
	elbClient := elbv2.NewFromConfig(cfg)

	// Get Load Balancer ARN from DNS name
	describeLBs, err := elbClient.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{})
	if err != nil {
		return fmt.Errorf("failed to describe load balancers: %v", err)
	}
	var lbARN string
	for _, lb := range describeLBs.LoadBalancers {
		if strings.EqualFold(aws.ToString(lb.DNSName), lbDNSName) {
			lbARN = aws.ToString(lb.LoadBalancerArn)
			break
		}
	}
	if lbARN == "" {
		return fmt.Errorf("could not find LB with DNS name: %s", lbDNSName)
	}

	// List listeners for the load balancer
	listenersOut, err := elbClient.DescribeListeners(ctx, &elbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		return fmt.Errorf("failed to describe listeners: %v", err)
	}

	// Get target groups attached to listeners
	targetGroupARNs := map[string]struct{}{}
	for _, listener := range listenersOut.Listeners {
		if len(targetGroupARNs) > 0 {
			break
		}
		for _, action := range listener.DefaultActions {
			if action.TargetGroupArn != nil {
				targetGroupARNs[aws.ToString(action.TargetGroupArn)] = struct{}{}
				break
			}
		}
	}

	// Count registered targets in target groups
	totalTargets := 0
	for tgARN := range targetGroupARNs {
		tgHealth, err := elbClient.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
			TargetGroupArn: aws.String(tgARN),
		})
		if err != nil {
			return fmt.Errorf("failed to describe target health for TG %s: %v", tgARN, err)
		}
		totalTargets += len(tgHealth.TargetHealthDescriptions)
	}

	// Verify count matches number of worker nodes
	if totalTargets != expectedTargets {
		return fmt.Errorf("target count mismatch: expected %d, got %d", expectedTargets, totalTargets)
	}
	return nil
}
