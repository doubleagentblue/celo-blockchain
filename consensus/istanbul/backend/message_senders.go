// Copyright 2017 The celo Authors
// This file is part of the celo library.
//
// The celo library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The celo library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the celo library. If not, see <http://www.gnu.org/licenses/>.

package backend

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// This function will return the peers with the addresses in the "destAddresses" parameter.
func (sb *Backend) getPeersFromDestAddresses(destAddresses []common.Address) map[enode.ID]consensus.Peer {
	var targets map[enode.ID]bool
	if destAddresses != nil {
		targets = make(map[enode.ID]bool)
		for _, addr := range destAddresses {
			if valNode, err := sb.valEnodeTable.GetNodeFromAddress(addr); valNode != nil && err == nil {
				targets[valNode.ID()] = true
			}
		}
	}
	return sb.broadcaster.FindPeers(targets, p2p.AnyPurpose)
}

// Multicast implements istanbul.Backend.Multicast
// Multicast will send the eth message (with the message's payload and msgCode field set to the params
// payload and ethMsgCode respectively) to the nodes with the signing address in the destAddresses param.
// If this node is proxied and destAddresses is not nil, the message will be wrapped
// in an istanbul.ForwardMessage to ensure the proxy sends it to the correct
// destAddresses.
func (sb *Backend) Multicast(destAddresses []common.Address, payload []byte, ethMsgCode uint64, sendToSelf bool) error {
	logger := sb.logger.New("func", "Multicast")

	var err error

	if sb.IsProxiedValidator() {
		if err := sb.proxyHandler.SendForwardMsg(destAddresses, ethMsgCode, payload); err != nil {
			return err
		}
	} else {
		destPeers := sb.getPeersFromDestAddresses(destAddresses)
		if len(destPeers) > 0 {
			err = sb.sendMsg(destPeers, payload, ethMsgCode)
		}
	}

	if sendToSelf {
		// Send to self.  Note that it will never be a wrapped version of the consensus message.
		msg := istanbul.MessageEvent{
			Payload: payload,
		}

		go sb.istanbulEventMux.Post(msg)
	}

	return err
}

// Gossip implements istanbul.Backend.Gossip
// Gossip will gossip the eth message to all connected peers
func (sb *Backend) Gossip(payload []byte, ethMsgCode uint64) error {
	logger := sb.logger.New("func", "Gossip")

	// Get all connected peers
	allPeers := sb.broadcaster.FindPeers(nil, p2p.AnyPurpose)

	// Mark that this node gossiped this message, so that it will ignore it if
	// one of it's peers sends the message to it.
	sb.markSelfGossipCache(payload)

	peersToSend := make([]consensus.Peer, 1)

	// Filter out peers that already sent us this gossip message
	for _, peer := range allPeers {
		nodePubKey := peer.Node().Pubkey()
		nodeAddr := crypto.PubkeyToAddress(*nodePubKey)
		if sb.checkPeerGossipCache(nodeAddr, payload) {
			logger.Trace("Peer already gossiped this message.  Not sending message to it", "peer", peer)
			continue
		} else {
			peersToSend = append(peersToSend, peer)
			sb.markPeerGossipCache(nodeAddr, payload)
		}
	}

	return sb.sendMsg(peersToSend, payload, ethMsgCode)
}

// sendMsg will send the eth message (with the message's payload and msgCode field set to the params
// payload and ethMsgCode respectively) to either the nodes with the signing address in the destAddresses param,
// or to all the connected peers with this node (if gossip parameter set to true).
func (sb *Backend) sendMsg(destPeers []consensus.Peer, payload []byte, ethMsgCode uint64) error {
	logger := sb.logger.New("func", "multicast")

	logger.Trace("Going to send a message", "peers", destPeers, "ethMsgCode", ethMsgCode)

	for _, peer := range destPeers {
		logger.Trace("Sending istanbul message to peer", "peer", peer)
		go peer.Send(ethMsgCode, payload)
	}

	return nil
}