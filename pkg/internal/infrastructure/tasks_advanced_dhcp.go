/*
 * Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package infrastructure

import (
	"fmt"
	"net/http"

	"github.com/pkg/errors"
	"github.com/vmware/go-vmware-nsxt/common"
	"github.com/vmware/go-vmware-nsxt/manager"
)

type advancedLookupEdgeClusterTask struct{ baseTask }

func newAdvancedLookupEdgeClusterTask() *advancedLookupEdgeClusterTask {
	return &advancedLookupEdgeClusterTask{baseTask{label: "edge cluster lookup (Advanced API)"}}
}

func (t *advancedLookupEdgeClusterTask) name(spec NSXTInfraSpec) *string { return &spec.EdgeClusterName }

func (t *advancedLookupEdgeClusterTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.EdgeClusterID)
}

func (t *advancedLookupEdgeClusterTask) Ensure(a *ensurer, spec NSXTInfraSpec, state *NSXTInfraState) error {
	name := spec.EdgeClusterName
	objList, _, err := a.nsxClient.NetworkTransportApi.ListEdgeClusters(a.nsxClient.Context, nil)
	if err != nil {
		return err
	}
	for _, obj := range objList.Results {
		if obj.DisplayName == name {
			state.AdvancedDHCP.EdgeClusterID = &obj.Id
			return nil
		}
	}
	return fmt.Errorf("not found: %s", name)
}

type advancedDHCPProfileTask struct{ baseTask }

func newAdvancedDHCPProfileTask() *advancedDHCPProfileTask {
	return &advancedDHCPProfileTask{baseTask{label: "DHCP profile (Advanced API)"}}
}

func (t *advancedDHCPProfileTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.PortID)
}

func (t *advancedDHCPProfileTask) Ensure(a *ensurer, spec NSXTInfraSpec, state *NSXTInfraState) error {
	profile := manager.DhcpProfile{
		DisplayName:   spec.FullClusterName(),
		Description:   description,
		EdgeClusterId: *state.AdvancedDHCP.EdgeClusterID,
		Tags:          spec.createCommonTags(),
	}

	if state.AdvancedDHCP.ProfileID != nil {
		oldProfile, resp, err := a.nsxClient.ServicesApi.ReadDhcpProfile(a.nsxClient.Context, *state.AdvancedDHCP.ProfileID)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			state.AdvancedDHCP.ProfileID = nil
			return t.Ensure(a, spec, state)
		}
		if err != nil {
			return readingErr(err)
		}
		if oldProfile.DisplayName != profile.DisplayName ||
			oldProfile.EdgeClusterId != profile.EdgeClusterId ||
			!equalCommonTags(oldProfile.Tags, profile.Tags) {
			_, resp, err := a.nsxClient.ServicesApi.UpdateDhcpProfile(a.nsxClient.Context, *state.AdvancedDHCP.ProfileID, profile)
			if err != nil {
				return updatingErr(err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("updating failed with unexpected HTTP status code %d", resp.StatusCode)
			}
		}
		return nil
	}

	createdProfile, resp, err := a.nsxClient.ServicesApi.CreateDhcpProfile(a.nsxClient.Context, profile)
	if err != nil {
		return creatingErr(err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("creating failed with unexpected HTTP status code %d", resp.StatusCode)
	}
	state.AdvancedDHCP.ProfileID = &createdProfile.Id
	return nil
}

func (t *advancedDHCPProfileTask) EnsureDeleted(a *ensurer, _ NSXTInfraSpec, state *NSXTInfraState) (bool, error) {
	if state.AdvancedDHCP.ProfileID == nil {
		return false, nil
	}
	resp, err := a.nsxClient.ServicesApi.DeleteDhcpProfile(a.nsxClient.Context, *state.AdvancedDHCP.ProfileID)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		state.AdvancedDHCP.ProfileID = nil
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state.AdvancedDHCP.ProfileID = nil
	return true, nil
}

type advancedDHCPServerTask struct{ baseTask }

func newAdvancedDHCPServerTask() *advancedDHCPServerTask {
	return &advancedDHCPServerTask{baseTask{label: "DHCP server (Advanced API)"}}
}

func (t *advancedDHCPServerTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.ServerID)
}

func (t *advancedDHCPServerTask) Ensure(a *ensurer, spec NSXTInfraSpec, state *NSXTInfraState) error {
	dhcpServerIP, err := cidrHostAndPrefix(spec.WorkersNetwork, 2)
	if err != nil {
		return errors.Wrapf(err, "DHCP server IP")
	}
	gatewayIP, err := cidrHost(spec.WorkersNetwork, 1)
	if err != nil {
		return errors.Wrapf(err, "gateway IP")
	}
	ipv4DhcpServer := manager.IPv4DhcpServer{
		DhcpServerIp:   dhcpServerIP,
		DnsNameservers: spec.DNSServers,
		GatewayIp:      gatewayIP,
	}

	server := manager.LogicalDhcpServer{
		Description:    description,
		DisplayName:    spec.FullClusterName(),
		Tags:           spec.createCommonTags(),
		DhcpProfileId:  *state.AdvancedDHCP.ProfileID,
		Ipv4DhcpServer: &ipv4DhcpServer,
	}

	if state.AdvancedDHCP.ServerID != nil {
		oldServer, resp, err := a.nsxClient.ServicesApi.ReadDhcpServer(a.nsxClient.Context, *state.AdvancedDHCP.ServerID)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			state.AdvancedDHCP.ServerID = nil
			return t.Ensure(a, spec, state)
		}
		if err != nil {
			return readingErr(err)
		}
		if oldServer.DisplayName != server.DisplayName ||
			oldServer.DhcpProfileId != server.DhcpProfileId ||
			oldServer.Ipv4DhcpServer == nil ||
			oldServer.Ipv4DhcpServer.DhcpServerIp != server.Ipv4DhcpServer.DhcpServerIp ||
			oldServer.Ipv4DhcpServer.GatewayIp != server.Ipv4DhcpServer.GatewayIp ||
			!equalOrderedStrings(oldServer.Ipv4DhcpServer.DnsNameservers, server.Ipv4DhcpServer.DnsNameservers) ||
			!equalCommonTags(oldServer.Tags, server.Tags) {
			_, resp, err := a.nsxClient.ServicesApi.UpdateDhcpServer(a.nsxClient.Context, *state.AdvancedDHCP.ServerID, server)
			if err != nil {
				return updatingErr(err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("updating failed with unexpected HTTP status code %d", resp.StatusCode)
			}
		}
		return nil
	}

	createdServer, resp, err := a.nsxClient.ServicesApi.CreateDhcpServer(a.nsxClient.Context, server)
	if err != nil {
		return creatingErr(err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("creating failed with unexpected HTTP status code %d", resp.StatusCode)
	}
	state.AdvancedDHCP.ServerID = &createdServer.Id
	return nil
}

func (t *advancedDHCPServerTask) EnsureDeleted(a *ensurer, _ NSXTInfraSpec, state *NSXTInfraState) (bool, error) {
	if state.AdvancedDHCP.ServerID == nil {
		return false, nil
	}
	resp, err := a.nsxClient.ServicesApi.DeleteDhcpServer(a.nsxClient.Context, *state.AdvancedDHCP.ServerID)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		state.AdvancedDHCP.ServerID = nil
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state.AdvancedDHCP.ServerID = nil
	return true, nil
}

type advancedLookupLogicalSwitchTask struct{ baseTask }

func newAdvancedLookupLogicalSwitchTask() *advancedLookupLogicalSwitchTask {
	return &advancedLookupLogicalSwitchTask{baseTask{label: "logical switch lookup (Advanced API)"}}
}

func (t *advancedLookupLogicalSwitchTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.LogicalSwitchID)
}

func (t *advancedLookupLogicalSwitchTask) Ensure(a *ensurer, _ NSXTInfraSpec, state *NSXTInfraState) error {
	result, resp, err := a.nsxClient.LogicalSwitchingApi.ListLogicalSwitches(a.nsxClient.Context, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("listing failed with unexpected HTTP status code %d", resp.StatusCode)
	}
	for _, obj := range result.Results {
		for _, tag := range obj.Tags {
			if tag.Scope == "policyPath" && tag.Tag == state.SegmentRef.Path {
				state.AdvancedDHCP.LogicalSwitchID = &obj.Id
				return nil
			}
		}
	}
	return fmt.Errorf("not found by segment path %s", state.SegmentRef.Path)
}

type advancedDHCPPortTask struct{ baseTask }

func newAdvancedDHCPPortTask() *advancedDHCPPortTask {
	return &advancedDHCPPortTask{baseTask{label: "DHCP port (Advanced API)"}}
}

func (t *advancedDHCPPortTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.PortID)
}

func (t *advancedDHCPPortTask) Ensure(a *ensurer, spec NSXTInfraSpec, state *NSXTInfraState) error {
	attachment := manager.LogicalPortAttachment{
		AttachmentType: "DHCP_SERVICE",
		Id:             *state.AdvancedDHCP.ServerID,
	}
	port := manager.LogicalPort{
		DisplayName:     spec.FullClusterName(),
		Description:     description,
		LogicalSwitchId: *state.AdvancedDHCP.LogicalSwitchID,
		AdminState:      "UP",
		Tags:            spec.createCommonTags(),
		Attachment:      &attachment,
	}

	if state.AdvancedDHCP.PortID != nil {
		oldPort, resp, err := a.nsxClient.LogicalSwitchingApi.GetLogicalPort(a.nsxClient.Context, *state.AdvancedDHCP.PortID)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			state.AdvancedDHCP.PortID = nil
			return t.Ensure(a, spec, state)
		}
		if err != nil {
			return readingErr(err)
		}
		if oldPort.DisplayName != port.DisplayName ||
			oldPort.LogicalSwitchId != port.LogicalSwitchId ||
			oldPort.AdminState != port.AdminState ||
			oldPort.Attachment == nil ||
			oldPort.Attachment.AttachmentType != port.Attachment.AttachmentType ||
			oldPort.Attachment.Id != port.Attachment.Id ||
			!equalCommonTags(oldPort.Tags, port.Tags) {
			_, resp, err := a.nsxClient.LogicalSwitchingApi.UpdateLogicalPort(a.nsxClient.Context, *state.AdvancedDHCP.PortID, port)
			if err != nil {
				return updatingErr(err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("updating failed with unexpected HTTP status code %d", resp.StatusCode)
			}
		}
		return nil
	}

	createdPort, resp, err := a.nsxClient.LogicalSwitchingApi.CreateLogicalPort(a.nsxClient.Context, port)
	if err != nil {
		return creatingErr(err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("creating failed with unexpected HTTP status code %d", resp.StatusCode)
	}
	state.AdvancedDHCP.PortID = &createdPort.Id
	return nil
}

func (t *advancedDHCPPortTask) EnsureDeleted(a *ensurer, _ NSXTInfraSpec, state *NSXTInfraState) (bool, error) {
	if state.AdvancedDHCP.PortID == nil {
		return false, nil
	}
	localVarOptionals := make(map[string]interface{})
	localVarOptionals["detach"] = true
	resp, err := a.nsxClient.LogicalSwitchingApi.DeleteLogicalPort(a.nsxClient.Context, *state.AdvancedDHCP.PortID, localVarOptionals)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		state.AdvancedDHCP.PortID = nil
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state.AdvancedDHCP.PortID = nil
	return true, nil
}

type advancedDHCPIPPoolTask struct{ baseTask }

func newAdvancedDHCPIPPoolTask() *advancedDHCPIPPoolTask {
	return &advancedDHCPIPPoolTask{baseTask{label: "DHCP IP pool (Advanced API)"}}
}

func (t *advancedDHCPIPPoolTask) reference(state *NSXTInfraState) *Reference {
	return toReference(state.AdvancedDHCP.IPPoolID)
}

func (t *advancedDHCPIPPoolTask) Ensure(a *ensurer, spec NSXTInfraSpec, state *NSXTInfraState) error {
	gatewayIP, err := cidrHost(spec.WorkersNetwork, 1)
	if err != nil {
		return errors.Wrapf(err, "gateway IP")
	}
	startIP, err := cidrHost(spec.WorkersNetwork, 10)
	if err != nil {
		return errors.Wrapf(err, "start IP of pool")
	}
	endIP, err := cidrHost(spec.WorkersNetwork, -1)
	if err != nil {
		return errors.Wrapf(err, "end IP of pool")
	}
	ipPoolRange := manager.IpPoolRange{
		Start: startIP,
		End:   endIP,
	}
	pool := manager.DhcpIpPool{
		DisplayName:      spec.FullClusterName(),
		Description:      description,
		GatewayIp:        gatewayIP,
		LeaseTime:        7200,
		ErrorThreshold:   98,
		WarningThreshold: 70,
		AllocationRanges: []manager.IpPoolRange{ipPoolRange},
		Tags:             spec.createCommonTags(),
	}

	if state.AdvancedDHCP.IPPoolID != nil {
		oldPool, resp, err := a.nsxClient.ServicesApi.ReadDhcpIpPool(a.nsxClient.Context, *state.AdvancedDHCP.ServerID, *state.AdvancedDHCP.IPPoolID)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			state.AdvancedDHCP.IPPoolID = nil
			return t.Ensure(a, spec, state)
		}
		if err != nil {
			return readingErr(err)
		}
		if oldPool.DisplayName != pool.DisplayName ||
			oldPool.GatewayIp != pool.GatewayIp ||
			oldPool.LeaseTime != pool.LeaseTime ||
			oldPool.ErrorThreshold == pool.ErrorThreshold ||
			oldPool.WarningThreshold == pool.WarningThreshold ||
			len(oldPool.AllocationRanges) != 1 ||
			oldPool.AllocationRanges[0].Start != pool.AllocationRanges[0].Start ||
			oldPool.AllocationRanges[0].End != pool.AllocationRanges[0].End ||
			!equalCommonTags(oldPool.Tags, pool.Tags) {
			_, resp, err := a.nsxClient.ServicesApi.UpdateDhcpIpPool(a.nsxClient.Context, *state.AdvancedDHCP.ServerID, *state.AdvancedDHCP.IPPoolID, pool)
			if err != nil {
				return updatingErr(err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("updating failed with unexpected HTTP status code %d", resp.StatusCode)
			}
		}
		return nil
	}

	createdPool, resp, err := a.nsxClient.ServicesApi.CreateDhcpIpPool(a.nsxClient.Context, *state.AdvancedDHCP.ServerID, pool)
	if err != nil {
		return creatingErr(err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("creating failed with unexpected HTTP status code %d", resp.StatusCode)
	}
	state.AdvancedDHCP.IPPoolID = &createdPool.Id
	return nil
}

func (t *advancedDHCPIPPoolTask) EnsureDeleted(a *ensurer, _ NSXTInfraSpec, state *NSXTInfraState) (bool, error) {
	if state.AdvancedDHCP.IPPoolID == nil {
		return false, nil
	}
	resp, err := a.nsxClient.ServicesApi.DeleteDhcpIpPool(a.nsxClient.Context, *state.AdvancedDHCP.ServerID, *state.AdvancedDHCP.IPPoolID)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		state.AdvancedDHCP.IPPoolID = nil
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state.AdvancedDHCP.IPPoolID = nil
	return true, nil
}

func equalCommonTags(a, b []common.Tag) bool {
	if len(a) != len(b) {
		return false
	}
	for _, ai := range a {
		found := false
		for _, bi := range b {
			if ai.Scope == bi.Scope && ai.Tag == bi.Tag {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
