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
	"time"

	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eservice "k8s.io/kubernetes/test/e2e/framework/service"
	admissionapi "k8s.io/pod-security-admission/api"
)

const (
	e2eTestPrefixLoadBalancer = "[cloud-provider-aws-e2e-openshift] loadbalancer"

	// featureGateAWSServiceLBNetworkSecurityGroup is the name of the feature gate
	// that enables managed security groups for Network Load Balancers.
	//
	// Future improvement: Use typed constant from github.com/openshift/api/features
	// when available: features.FeatureGateAWSServiceLBNetworkSecurityGroup
	featureGateAWSServiceLBNetworkSecurityGroup = "AWSServiceLBNetworkSecurityGroup"

	annotationLBType = "service.beta.kubernetes.io/aws-load-balancer-type"

	cloudConfigNamespace = "openshift-cloud-controller-manager"
	cloudConfigName      = "cloud-conf"
	cloudConfigKey       = "cloud.conf"
)

// TestAWSServiceLBNetworkSecurityGroup validates the AWSServiceLBNetworkSecurityGroup feature gate functionality.
//
// This test suite validates that Network Load Balancers (NLB) are properly configured with security groups
// when the AWSServiceLBNetworkSecurityGroup feature gate is enabled. This feature allows the cloud controller
// to manage security groups for NLB services, improving security posture and reducing manual configuration.
//
// All tests automatically skip if the AWSServiceLBNetworkSecurityGroup feature gate is not enabled.
var _ = Describe(fmt.Sprintf("%s NLB feature %s", e2eTestPrefixLoadBalancer, featureGateAWSServiceLBNetworkSecurityGroup), func() {
	f := framework.NewDefaultFramework("cloud-provider-aws")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var cs clientset.Interface
	var ns *v1.Namespace

	BeforeEach(func() {
		cs = f.ClientSet
		ns = f.Namespace
	})

	// Checker function to verify if the feature gate is enabled for the group of tests for feature AWSServiceLBNetworkSecurityGroup.
	isNLBFeatureEnabled := func(ctx context.Context) {
		By(fmt.Sprintf("checking if %s feature gate is enabled", featureGateAWSServiceLBNetworkSecurityGroup))
		featureEnabled, err := isFeatureEnabled(ctx, featureGateAWSServiceLBNetworkSecurityGroup)
		framework.ExpectNoError(err, fmt.Sprintf("failed to check if %s feature is enabled", featureGateAWSServiceLBNetworkSecurityGroup))
		if !featureEnabled {
			Skip(fmt.Sprintf("%s feature gate is not enabled", featureGateAWSServiceLBNetworkSecurityGroup))
		}
	}

	// Test: [cloud-provider-aws-e2e-openshift] loadbalancer NLB feature AWSServiceLBNetworkSecurityGroup should have NLBSecurityGroupMode with 'Managed' value in cloud-config
	//
	// Validates that the cloud controller manager's configuration contains the proper NLBSecurityGroupMode setting
	// when the AWSServiceLBNetworkSecurityGroup feature gate is enabled.
	//
	// Prerequisites:
	//   - AWSServiceLBNetworkSecurityGroup feature gate is enabled
	//
	// Expected Results:
	//   - ConfigMap exists and contains cloud.conf key
	//   - Configuration includes: NLBSecurityGroupMode set to 'Managed'
	//	 - The test must fail if the feature gate is enabled and the configuration does not include NLBSecurityGroupMode set to 'Managed'
	//   - The test must skip if the feature gate is not enabled
	It("should have NLBSecurityGroupMode with 'Managed value in cloud-config", func(ctx context.Context) {
		isNLBFeatureEnabled(ctx)

		By("getting cloud-config ConfigMap from openshift-cloud-controller-manager namespace")
		cm, err := cs.CoreV1().ConfigMaps(cloudConfigNamespace).Get(ctx, cloudConfigName, metav1.GetOptions{})
		framework.ExpectNoError(err, "failed to get cloud-config ConfigMap")

		By("checking if cloud.conf key exists in ConfigMap")
		cloudConf, exists := cm.Data[cloudConfigKey]
		Expect(exists).To(BeTrue(), "cloud.conf key not found in ConfigMap")

		By("verifying NLBSecurityGroupMode is present in cloud config")
		Expect(cloudConf).To(ContainSubstring("NLBSecurityGroupMode"),
			"NLBSecurityGroupMode must be present in cloud-config when feature gate is enabled")

		By("verifying NLBSecurityGroupMode is set to Managed")
		Expect(cloudConf).To(MatchRegexp(`NLBSecurityGroupMode\s*=\s*Managed`),
			"NLBSecurityGroupMode must be set to 'Managed' in cloud-config when feature gate is enabled")

		framework.Logf("Successfully validated cloud-config contains NLBSecurityGroupMode = Managed")
	})

	// Test: [cloud-provider-aws-e2e-openshift] loadbalancer NLB feature AWSServiceLBNetworkSecurityGroup should create NLB service with security group attached
	//
	// Creates a new Service type loadBalancer Network Load Balancer (NLB) and validates that security groups are
	// automatically attached to the NLB when the AWSServiceLBNetworkSecurityGroup feature is enabled.
	//
	// Prerequisites:
	//   - AWSServiceLBNetworkSecurityGroup feature gate is enabled
	//
	// Expected Results:
	//   - Service type loadBalancer Network Load Balancer (NLB) is created successfully
	//   - Backend pods start and become ready
	//   - Load balancer has one or more security groups attached when NLBSecurityGroupMode = Managed
	//   - The test must fail if the feature gate is enabled and the NLB does not have security groups attached
	//   - The test must skip if the feature gate is not enabled
	It("should create NLB service with security group attached", func(ctx context.Context) {
		isNLBFeatureEnabled(ctx)

		By("creating test service and deployment configuration")
		serviceName := "nlb-sg-test"
		loadBalancerCreateTimeout := e2eservice.GetServiceLoadBalancerCreationTimeout(ctx, cs)
		framework.Logf("AWS load balancer timeout: %s", loadBalancerCreateTimeout)

		jig := e2eservice.NewTestJig(cs, ns.Name, serviceName)

		By("creating NLB LoadBalancer service")
		svc := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: jig.Namespace,
				Name:      jig.Name,
				Labels:    jig.Labels,
				Annotations: map[string]string{
					annotationLBType: "nlb",
				},
			},
			Spec: v1.ServiceSpec{
				Type:            v1.ServiceTypeLoadBalancer,
				SessionAffinity: v1.ServiceAffinityNone,
				Selector:        jig.Labels,
				Ports: []v1.ServicePort{
					{
						Name:       "http",
						Protocol:   v1.ProtocolTCP,
						Port:       80,
						TargetPort: intstr.FromInt(8080),
					},
				},
			},
		}

		_, err := jig.Client.CoreV1().Services(jig.Namespace).Create(ctx, svc, metav1.CreateOptions{})
		framework.ExpectNoError(err, "failed to create LoadBalancer Service")

		By("waiting for AWS load balancer provisioning")
		svc, err = jig.WaitForLoadBalancer(ctx, loadBalancerCreateTimeout)
		framework.ExpectNoError(err, "LoadBalancer provisioning failed")

		// TODO: create a backend and test it. Upstream (both cloud-provider-aws/e2e and kubernetes/e2e)
		// provides helpers to create backends and frontends to test HTTP reachability.

		By("extracting load balancer DNS name")
		Expect(len(svc.Status.LoadBalancer.Ingress)).To(BeNumerically(">", 0),
			"no ingress entry found in LoadBalancer status")
		lbDNS := svc.Status.LoadBalancer.Ingress[0].Hostname
		framework.Logf("Load balancer DNS: %s", lbDNS)

		By("getting AWS ELB client and finding load balancer")
		elbClient, err := getAWSClientLoadBalancer(ctx)
		framework.ExpectNoError(err, "failed to create AWS ELB client")

		foundLB, err := getAWSLoadBalancerFromDNSName(ctx, elbClient, lbDNS)
		framework.ExpectNoError(err, "failed to find load balancer with DNS name %s", lbDNS)
		Expect(foundLB).NotTo(BeNil(), "found load balancer is nil")

		By("verifying security groups are attached to the NLB")
		Expect(len(foundLB.SecurityGroups)).To(BeNumerically(">", 0),
			"load balancer should have security groups attached when NLBSecurityGroupMode = Managed")

		framework.Logf("Successfully validated that load balancer has %d security group(s) attached", len(foundLB.SecurityGroups))
		for i, sg := range foundLB.SecurityGroups {
			framework.Logf("  Security Group %d: %s", i+1, sg)
		}

		By("cleaning up: converting service to ClusterIP")
		_, err = jig.UpdateService(ctx, func(s *v1.Service) {
			s.Spec.Type = v1.ServiceTypeClusterIP
		})
		framework.ExpectNoError(err)

		By("cleaning up: waiting for load balancer destruction")
		_, err = jig.WaitForLoadBalancerDestroy(ctx, lbDNS, 80, loadBalancerCreateTimeout)
		framework.ExpectNoError(err)
		framework.Logf("Load balancer destroyed successfully")
	})

	// Test: [cloud-provider-aws-e2e-openshift] loadbalancer NLB feature AWSServiceLBNetworkSecurityGroup should have security groups attached to default ingress controller NLB
	//
	// Validates that the default OpenShift ingress controller's Service type loadBalancer Network Load Balancer (NLB) has security groups
	// attached when the AWSServiceLBNetworkSecurityGroup feature is enabled and the router uses NLB type.
	//
	// Prerequisites:
	//   - AWSServiceLBNetworkSecurityGroup feature gate is enabled
	//   - The default ingress controller is using NLB type
	//
	// Expected Result:
	//   - Default router service exists and is of type LoadBalancer
	//   - Service uses NLB type (service.beta.kubernetes.io/aws-load-balancer-type: nlb)
	//   - Load balancer is in Active state
	//   - Load balancer has one or more security groups attached
	//
	// Note: Skips if the default ingress controller is not using NLB type
	It("should have security groups attached to default ingress controller NLB", func(ctx context.Context) {
		isNLBFeatureEnabled(ctx)
		By("getting default ingress controller service")
		ingressNamespace := "openshift-ingress"
		ingressServiceName := "router-default"

		var svc *v1.Service
		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
			s, err := cs.CoreV1().Services(ingressNamespace).Get(ctx, ingressServiceName, metav1.GetOptions{})
			if err != nil {
				framework.Logf("Failed to get service %s/%s: %v", ingressNamespace, ingressServiceName, err)
				return false, nil
			}
			svc = s
			return true, nil
		})
		framework.ExpectNoError(err, "failed to get default ingress controller service")

		By("verifying service is of type LoadBalancer")
		Expect(svc.Spec.Type).To(Equal(v1.ServiceTypeLoadBalancer),
			"default ingress controller service should be of type LoadBalancer")

		By("checking if service has LoadBalancer ingress hostname")
		Expect(len(svc.Status.LoadBalancer.Ingress)).To(BeNumerically(">", 0),
			"no ingress entry found in LoadBalancer status")

		lbDNS := svc.Status.LoadBalancer.Ingress[0].Hostname
		Expect(lbDNS).NotTo(BeEmpty(), "LoadBalancer hostname should not be empty")
		framework.Logf("Default ingress controller load balancer DNS: %s", lbDNS)

		By("checking if the service is NLB type")
		lbType, hasAnnotation := svc.Annotations[annotationLBType]
		if !hasAnnotation || lbType != "nlb" {
			Skip("Default ingress controller is not using NLB type, skipping test")
		}
		framework.Logf("Default ingress controller is using NLB type")

		By("getting AWS ELB client and finding load balancer")
		elbClient, err := getAWSClientLoadBalancer(ctx)
		framework.ExpectNoError(err, "failed to create AWS ELB client")

		var foundLB *elbv2types.LoadBalancer
		err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(pollCtx context.Context) (bool, error) {
			lb, err := getAWSLoadBalancerFromDNSName(pollCtx, elbClient, lbDNS)
			if err != nil {
				framework.Logf("Failed to find load balancer with DNS %s: %v", lbDNS, err)
				return false, nil
			}
			if lb != nil && lb.State != nil && lb.State.Code == elbv2types.LoadBalancerStateEnumActive {
				foundLB = lb
				return true, nil
			}
			framework.Logf("Load balancer not yet active, current state: %v", lb.State)
			return false, nil
		})
		framework.ExpectNoError(err, "failed to find active load balancer")
		Expect(foundLB).NotTo(BeNil(), "found load balancer is nil")

		By("verifying security groups are attached to the default ingress NLB")
		Expect(len(foundLB.SecurityGroups)).To(BeNumerically(">", 0),
			"default ingress load balancer should have security groups attached when NLBSecurityGroupMode = Managed")

		framework.Logf("Successfully validated that default ingress load balancer has %d security group(s) attached", len(foundLB.SecurityGroups))
		for i, sg := range foundLB.SecurityGroups {
			framework.Logf("  Security Group %d: %s", i+1, sg)
		}
	})

})
