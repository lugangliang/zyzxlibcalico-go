// Copyright (c) 2019-2021 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package updateprocessors

import (
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/libcalico-go/lib/backend/watchersyncer"
	cresources "github.com/projectcalico/libcalico-go/lib/resources"

	wg "golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	cnet "github.com/projectcalico/libcalico-go/lib/net"
)

// Create a new SyncerUpdateProcessor to sync Node data in v1 format for
// consumption by Felix.
func NewFelixNodeUpdateProcessor(usePodCIDR bool) watchersyncer.SyncerUpdateProcessor {
	return &FelixNodeUpdateProcessor{
		usePodCIDR:      usePodCIDR,
		nodeCIDRTracker: newNodeCIDRTracker(),
	}
}

// FelixNodeUpdateProcessor implements the SyncerUpdateProcessor interface.
// This converts the v3 node configuration into the v1 data types consumed by confd.
type FelixNodeUpdateProcessor struct {
	usePodCIDR      bool
	nodeCIDRTracker nodeCIDRTracker
}

func (c *FelixNodeUpdateProcessor) Process(kvp *model.KVPair) ([]*model.KVPair, error) {
	// Extract the name.
	name, err := c.extractName(kvp.Key)
	if err != nil {
		return nil, err
	}

	// Extract the separate bits of BGP config - these are stored as separate keys in the
	// v1 model.  For a delete these will all be nil.  If we fail to convert any value then
	// just treat that as a delete on the underlying key and return the error alongside
	// the updates.
	var ipv4, ipv6, ipv4Tunl, vxlanTunlIpv4, vxlanTunlIpv6, vxlanTunlMacV4, vxlanTunlMacV6, wgConfig interface{}
	var node *apiv3.Node
	var ok bool
	if kvp.Value != nil {
		node, ok = kvp.Value.(*apiv3.Node)
		if !ok {
			return nil, errors.New("Incorrect value type - expecting resource of kind Node")
		}

		if bgp := node.Spec.BGP; bgp != nil {
			var ip *cnet.IP
			var cidr *cnet.IPNet

			// Parse the IPv4 address, Felix expects this as a HostIPKey.  If we fail to parse then
			// treat as a delete (i.e. leave ipv4 as nil).
			if len(bgp.IPv4Address) != 0 {
				ip, cidr, err = cnet.ParseCIDROrIP(bgp.IPv4Address)
				if err == nil {
					log.WithFields(log.Fields{"ip": ip, "cidr": cidr}).Debug("Parsed IPv4 address")
					ipv4 = ip
				} else {
					log.WithError(err).WithField("IPv4Address", bgp.IPv4Address).Warn("Failed to parse IPv4Address")
				}
			}
			if len(bgp.IPv6Address) != 0 {
				ip, cidr, err = cnet.ParseCIDROrIP(bgp.IPv6Address)
				if err == nil {
					log.WithFields(log.Fields{"ip": ip, "cidr": cidr}).Debug("Parsed IPv6 address")
					ipv4 = ip
				} else {
					log.WithError(err).WithField("IPv6Address", bgp.IPv6Address).Warn("Failed to parse IPv6Address")
				}
			}

			// Parse the IPv4 IPIP tunnel address, Felix expects this as a HostConfigKey.  If we fail to parse then
			// treat as a delete (i.e. leave ipv4Tunl as nil).
			if len(bgp.IPv4IPIPTunnelAddr) != 0 {
				ip := cnet.ParseIP(bgp.IPv4IPIPTunnelAddr)
				if ip != nil {
					log.WithField("ip", ip).Debug("Parsed IPIP tunnel address")
					ipv4Tunl = ip.String()
				} else {
					log.WithField("IPv4IPIPTunnelAddr", bgp.IPv4IPIPTunnelAddr).Warn("Failed to parse IPv4IPIPTunnelAddr")
					err = fmt.Errorf("failed to parsed IPv4IPIPTunnelAddr as an IP address")
				}
			}
		}
		// Look for internal node address, if BGP is not running
		if ipv4 == nil {
			ip, _ := cresources.FindNodeIPv4Address(node, apiv3.InternalIP)
			if ip != nil {
				ipv4 = ip
			}
		}
		if ipv4 == nil {
			ip, _ := cresources.FindNodeIPv4Address(node, apiv3.ExternalIP)
			if ip != nil {
				ipv4 = ip
			}
		}
		if ipv6 == nil {
			ip, _ := cresources.FindNodeAddress(node, apiv3.InternalIP)
			if ip != nil {
				ipv6 = ip
			}
		}
		if ipv6 == nil {
			ip, _ := cresources.FindNodeAddress(node, apiv3.ExternalIP)
			if ip != nil {
				ipv6 = ip
			}
		}

		// Parse the IPv4 VXLAN tunnel address, Felix expects this as a HostConfigKey.  If we fail to parse then
		// treat as a delete (i.e. leave ipv4Tunl as nil).
		if len(node.Spec.IPv4VXLANTunnelAddr) != 0 {
			ip := cnet.ParseIP(node.Spec.IPv4VXLANTunnelAddr)
			if ip != nil {
				log.WithField("ip", ip).Debug("Parsed VXLAN tunnel IPv4 address")
				vxlanTunlIpv4 = ip.String()
			} else {
				log.WithField("IPv4VXLANTunnelAddr", node.Spec.IPv4VXLANTunnelAddr).Warn("Failed to parse IPv4VXLANTunnelAddr")
				err = fmt.Errorf("failed to parsed IPv4VXLANTunnelAddr as an IP address")
			}
		}

		// Parse the IPv6 VXLAN tunnel address, Felix expects this as a HostConfigKey.  If we fail to parse then
		// treat as a delete (i.e. leave ipv4Tunl as nil).
		if len(node.Spec.IPv6VXLANTunnelAddr) != 0 {
			ip := cnet.ParseIP(node.Spec.IPv6VXLANTunnelAddr)
			if ip != nil {
				log.WithField("ip", ip).Debug("Parsed VXLAN tunnel address")
				vxlanTunlIpv6 = ip.String()
			} else {
				log.WithField("IPv6VXLANTunnelAddr", node.Spec.IPv6VXLANTunnelAddr).Warn("Failed to parse IPv6VXLANTunnelAddr")
				err = fmt.Errorf("failed to parsed IPv6VXLANTunnelAddr as an IP address")
			}
		}

		// Parse the VXLAN tunnel MAC address, Felix expects this as a HostConfigKey.  If we fail to parse then
		// treat as a delete (i.e. leave ipv4Tunl as nil).
		if len(node.Spec.VXLANTunnelMACV4Addr) != 0 {
			macV4 := node.Spec.VXLANTunnelMACV4Addr
			if macV4 != "" {
				log.WithField("mac v4 addr", macV4).Debug("Parsed VXLAN tunnel MAC V4 address")
				vxlanTunlMacV4 = macV4
			} else {
				log.WithField("VXLANTunnelMACV4Addr", node.Spec.VXLANTunnelMACV4Addr).Warn("VXLANTunnelMACV4Addr not populated")
				err = fmt.Errorf("failed to update VXLANTunnelMACAddr")
			}
		}

		if len(node.Spec.VXLANTunnelMACV6Addr) != 0 {
			macV6 := node.Spec.VXLANTunnelMACV6Addr
			if macV6 != "" {
				log.WithField("mac v6 addr", macV6).Debug("Parsed VXLAN tunnel MAC V6 address")
				vxlanTunlMacV6 = macV6
			} else {
				log.WithField("VXLANTunnelMACV6Addr", node.Spec.VXLANTunnelMACV6Addr).Warn("VXLANTunnelMACV6Addr not populated")
				err = fmt.Errorf("failed to update VXLANTunnelMACV6Addr")
			}
		}

		var wgIfaceIpv4Addr *cnet.IP
		var wgPubKey string
		if wgSpec := node.Spec.Wireguard; wgSpec != nil {
			if len(wgSpec.InterfaceIPv4Address) != 0 {
				wgIfaceIpv4Addr = cnet.ParseIP(wgSpec.InterfaceIPv4Address)
				if wgIfaceIpv4Addr != nil {
					log.WithField("InterfaceIPv4Addr", wgIfaceIpv4Addr).Debug("Parsed Wireguard interface address")
				} else {
					log.WithField("InterfaceIPv4Addr", wgSpec.InterfaceIPv4Address).Warn("Failed to parse InterfaceIPv4Address")
					err = fmt.Errorf("failed to parse InterfaceIPv4Address as an IP address")
				}
			}
		}
		if wgPubKey = node.Status.WireguardPublicKey; wgPubKey != "" {
			_, err := wg.ParseKey(wgPubKey)
			if err == nil {
				log.WithField("public-key", wgPubKey).Debug("Parsed Wireguard public-key")
			} else {
				log.WithField("WireguardPublicKey", wgPubKey).Warn("Failed to parse Wireguard public-key")
				err = fmt.Errorf("failed to parse PublicKey as Wireguard public-key")
			}
		}

		// If either of interface address or public-key is set, set the WireguardKey value.
		// If we failed to parse both the values, leave the WireguardKey value empty.
		if wgIfaceIpv4Addr != nil || wgPubKey != "" {
			wgConfig = &model.Wireguard{InterfaceIPv4Addr: wgIfaceIpv4Addr, PublicKey: wgPubKey}
		}
	}

	kvps := []*model.KVPair{
		{
			Key: model.HostIPKey{
				Hostname: name,
			},
			Value:    ipv4,
			Revision: kvp.Revision,
		},
		//{
		//	Key: model.HostIPKey{
		//		Hostname: name,
		//	},
		//	Value:    ipv6,
		//	Revision: kvp.Revision,
		//},
		{
			Key: model.HostConfigKey{
				Hostname: name,
				Name:     "IpInIpTunnelAddr",
			},
			Value:    ipv4Tunl,
			Revision: kvp.Revision,
		},
		{
			Key: model.HostConfigKey{
				Hostname: name,
				Name:     "IPv4VXLANTunnelAddr",
			},
			Value:    vxlanTunlIpv4,
			Revision: kvp.Revision,
		},
		{
			Key: model.HostConfigKey{
				Hostname: name,
				Name:     "IPv6VXLANTunnelAddr",
			},
			Value:    vxlanTunlIpv6,
			Revision: kvp.Revision,
		},
		{
			Key: model.HostConfigKey{
				Hostname: name,
				Name:     "VXLANTunnelMACV6Addr",
			},
			Value:    vxlanTunlMacV6,
			Revision: kvp.Revision,
		},
		{
			Key: model.HostConfigKey{
				Hostname: name,
				Name:     "VXLANTunnelMACV4Addr",
			},
			Value:    vxlanTunlMacV4,
			Revision: kvp.Revision,
		},
		{
			// Include the original node KVP info as a separate update. Note we do not use the node value here because
			// a nil interface is different to a nil pointer. Felix and other code assumes a nil Value is a delete, so
			// preserve that relationship here.
			Key: model.ResourceKey{
				Name: name,
				Kind: apiv3.KindNode,
			},
			Value:    kvp.Value,
			Revision: kvp.Revision,
		},
		{
			Key: model.WireguardKey{
				NodeName: name,
			},
			Value:    wgConfig,
			Revision: kvp.Revision,
		},
	}

	if c.usePodCIDR {
		// If we're using host-local IPAM based off the Kubernetes node PodCIDR, then
		// we need to send Blocks based on the CIDRs to felix.
		log.Debug("Using pod cidr")
		var currentPodCIDRs []string
		if node != nil {
			currentPodCIDRs = node.Status.PodCIDRs
		}
		toRemove := c.nodeCIDRTracker.SetNodeCIDRs(name, currentPodCIDRs)
		log.Debugf("Current CIDRS: %s", currentPodCIDRs)
		log.Debugf("Old CIDRS: %s", toRemove)

		// Send deletes for any CIDRs which are no longer present.
		for _, c := range toRemove {
			_, cidr, err := cnet.ParseCIDR(c)
			if err != nil {
				log.WithError(err).WithField("CIDR", c).Warn("Failed to parse Node PodCIDR")
				continue
			}
			kvps = append(kvps, &model.KVPair{
				Key:      model.BlockKey{CIDR: *cidr},
				Value:    nil,
				Revision: kvp.Revision,
			})
		}

		// Send updates for any CIDRs which are still present.
		for _, c := range currentPodCIDRs {
			_, cidr, err := cnet.ParseCIDR(c)
			if err != nil {
				log.WithError(err).WithField("CIDR", c).Warn("Failed to parse Node PodCIDR")
				continue
			}

			aff := fmt.Sprintf("host:%s", name)
			kvps = append(kvps, &model.KVPair{
				Key:      model.BlockKey{CIDR: *cidr},
				Value:    &model.AllocationBlock{CIDR: *cidr, Affinity: &aff},
				Revision: kvp.Revision,
			})
		}
	}

	return kvps, err
}

// Sync is restarting - nothing to do for this processor.
func (c *FelixNodeUpdateProcessor) OnSyncerStarting() {
	log.Debug("Sync starting called on Felix node update processor")
}

func (c *FelixNodeUpdateProcessor) extractName(k model.Key) (string, error) {
	rk, ok := k.(model.ResourceKey)
	if !ok || rk.Kind != apiv3.KindNode {
		return "", errors.New("Incorrect key type - expecting resource of kind Node")
	}
	return rk.Name, nil
}
