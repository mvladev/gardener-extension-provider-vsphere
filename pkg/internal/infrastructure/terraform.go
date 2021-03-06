/*
 * Copyright 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 *
 */

package infrastructure

import (
	"fmt"
	"path/filepath"

	api "github.com/gardener/gardener-extension-provider-vsphere/pkg/apis/vsphere"
	"github.com/gardener/gardener-extension-provider-vsphere/pkg/apis/vsphere/helper"
	"github.com/gardener/gardener-extension-provider-vsphere/pkg/vsphere"

	"github.com/gardener/gardener-extensions/pkg/terraformer"
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/chartrenderer"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// TerraformOutputKeyNetworkName is the private worker network.
	TerraformOutputKeyNetworkName = "network_name"
	// TerraformOutputKeyLogicalRouterId is id of the logical T1 router
	TerraformOutputKeyLogicalRouterId = "logical_router_id"
	// TerraformOutputKeyLogicalSwitchId is id of the logical switch
	TerraformOutputKeyLogicalSwitchId = "logical_switch_id"
)

// ComputeTerraformerChartValues computes the values for the vSphere Terraformer chart.
func ComputeTerraformerChartValues(
	infra *extensionsv1alpha1.Infrastructure,
	config *api.InfrastructureConfig,
	cloudProfileConfig *api.CloudProfileConfig,
	networking corev1beta1.Networking,
) (map[string]interface{}, error) {
	region := helper.FindRegion(infra.Spec.Region, cloudProfileConfig)
	if region == nil {
		return nil, fmt.Errorf("region %q not found in cloud profile", infra.Spec.Region)
	}
	if len(region.Zones) == 0 {
		return nil, fmt.Errorf("region %q has no zones in cloud profile", infra.Spec.Region)
	}
	dnsServers := cloudProfileConfig.DNSServers
	if len(region.DNSServers) > 0 {
		dnsServers = region.DNSServers
	}

	return map[string]interface{}{
		"nsxt": map[string]interface{}{
			"host":               region.NSXTHost,
			"insecure":           region.NSXTInsecureSSL,
			"transportZone":      region.TransportZone,
			"logicalTier0Router": region.LogicalTier0Router,
			"edgeCluster":        region.EdgeCluster,
			"snatIpPool":         region.SNATIPPool,
			"namePrefix":         cloudProfileConfig.NamePrefix,
			"dnsServers":         dnsServers,
		},
		"sshPublicKey": string(infra.Spec.SSHPublicKey),
		"clusterName":  infra.Namespace,
		"networks": map[string]interface{}{
			"worker": *networking.Nodes,
		},
	}, nil
}

// RenderTerraformerChart renders the vsphere-infra chart with the given values.
func RenderTerraformerChart(
	renderer chartrenderer.Interface,
	infra *extensionsv1alpha1.Infrastructure,
	config *api.InfrastructureConfig,
	cloudProfileConfig *api.CloudProfileConfig,
	networking corev1beta1.Networking,
) (*TerraformFiles, error) {
	values, err := ComputeTerraformerChartValues(infra, config, cloudProfileConfig, networking)
	if err != nil {
		return nil, err
	}

	release, err := renderer.Render(filepath.Join(vsphere.InternalChartsPath, "vsphere-infra"), "vsphere-infra", infra.Namespace, values)
	if err != nil {
		return nil, err
	}

	return &TerraformFiles{
		Main:      release.FileContent("main.tf"),
		Variables: release.FileContent("variables.tf"),
		TFVars:    []byte(release.FileContent("terraform.tfvars")),
	}, nil
}

// TerraformFiles are the files that have been rendered from the infrastructure chart.
type TerraformFiles struct {
	Main      string
	Variables string
	TFVars    []byte
}

// terraformState is the Terraform state for an infrastructure.
type terraformState struct {
	// NetworkName is the private worker network.
	NetworkName     string
	LogicalRouterId string
	LogicalSwitchId string
}

// extractTerraformState extracts the terraformState from the given Terraformer.
func extractTerraformState(tf terraformer.Terraformer) (*terraformState, error) {
	outputKeys := []string{
		TerraformOutputKeyNetworkName,
		TerraformOutputKeyLogicalRouterId,
		TerraformOutputKeyLogicalSwitchId,
	}

	vars, err := tf.GetStateOutputVariables(outputKeys...)
	if err != nil {
		return nil, err
	}

	state := &terraformState{
		NetworkName:     vars[TerraformOutputKeyNetworkName],
		LogicalRouterId: vars[TerraformOutputKeyLogicalRouterId],
		LogicalSwitchId: vars[TerraformOutputKeyLogicalSwitchId],
	}
	return state, nil
}

// ComputeStatus computes the status based on the Terraformer and the given InfrastructureConfig.
func ComputeStatus(tf terraformer.Terraformer, cloudProfileConfig *api.CloudProfileConfig, regionName string) (*api.InfrastructureStatus, error) {
	state, err := extractTerraformState(tf)
	if err != nil {
		return nil, err
	}

	region := helper.FindRegion(regionName, cloudProfileConfig)
	if region == nil {
		return nil, fmt.Errorf("region %q not found in cloud profile", regionName)
	}

	zoneConfigs := map[string]api.ZoneConfig{}
	for _, z := range region.Zones {
		datacenter := region.Datacenter
		if z.Datacenter != nil {
			datacenter = z.Datacenter
		}
		if datacenter == nil {
			return nil, fmt.Errorf("datacenter not set in zone %s", z.Name)
		}
		datastore := region.Datastore
		datastoreCluster := region.DatastoreCluster
		if z.Datastore != nil {
			datastore = z.Datastore
			datastoreCluster = nil
		} else if z.DatastoreCluster != nil {
			datastore = nil
			datastoreCluster = z.DatastoreCluster
		}
		zoneConfigs[z.Name] = api.ZoneConfig{
			Datacenter:       safe(datacenter),
			ComputeCluster:   safe(z.ComputeCluster),
			ResourcePool:     safe(z.ResourcePool),
			HostSystem:       safe(z.HostSystem),
			Datastore:        safe(datastore),
			DatastoreCluster: safe(datastoreCluster),
		}
	}

	status := &api.InfrastructureStatus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: api.SchemeGroupVersion.String(),
			Kind:       "InfrastructureStatus",
		},
		Network:         state.NetworkName,
		LogicalRouterId: state.LogicalRouterId,
		LogicalSwitchId: state.LogicalSwitchId,
		VsphereConfig: api.VsphereConfig{
			Folder:      cloudProfileConfig.Folder,
			Region:      region.Name,
			ZoneConfigs: zoneConfigs,
		},
	}
	return status, nil
}

func safe(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
