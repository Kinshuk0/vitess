/*
Copyright 2021 The Vitess Authors.

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

package reparentutil

import (
	"context"

	"vitess.io/vitess/go/event"

	"vitess.io/vitess/go/vt/logutil"
	logutilpb "vitess.io/vitess/go/vt/proto/logutil"
	"vitess.io/vitess/go/vt/topotools/events"
	"vitess.io/vitess/go/vt/vttablet/tmclient"
)

// EmergencyReparenter2 performs EmergencyReparentShard operations.
type EmergencyReparenter2 struct {
	tmc    tmclient.TabletManagerClient
	logger logutil.Logger
}

// NewEmergencyReparenter2 returns a new EmergencyReparenter object, ready to
// perform EmergencyReparentShard operations using the given topo.Server,
// TabletManagerClient, and logger.
//
// Providing a nil logger instance is allowed.
func NewEmergencyReparenter2(tmc tmclient.TabletManagerClient, logger logutil.Logger) *EmergencyReparenter2 {
	erp := EmergencyReparenter2{
		tmc:    tmc,
		logger: logger,
	}

	if erp.logger == nil {
		// Create a no-op logger so we can call functions on er.logger without
		// needed to constantly check for non-nil.
		erp.logger = logutil.NewCallbackLogger(func(*logutilpb.Event) {})
	}

	return &erp
}

// ReparentShard performs the EmergencyReparentShard operation on the given
// keyspace and shard.
func (erp *EmergencyReparenter2) ReparentShard(ctx context.Context, reparentFunctions ReparentFunctions) (*events.Reparent, error) {
	ctx, unlock, err := reparentFunctions.LockShard(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock(&err)

	ev := &events.Reparent{}
	defer func() {
		switch err {
		case nil:
			event.DispatchUpdate(ev, "finished EmergencyReparentShard")
		default:
			event.DispatchUpdate(ev, "failed EmergencyReparentShard: "+err.Error())
		}
	}()

	err = erp.reparentShardLocked(ctx, ev, reparentFunctions)

	return ev, nil
}

func (erp *EmergencyReparenter2) reparentShardLocked(ctx context.Context, ev *events.Reparent, reparentFunctions ReparentFunctions) error {

	if reparentFunctions.CheckIfFixed() {
		return nil
	}

	ts := reparentFunctions.GetTopoServer()
	shardInfo, err := ts.GetShard(ctx, reparentFunctions.GetKeyspace(), reparentFunctions.GetShard())
	if err != nil {
		return err
	}
	ev.ShardInfo = *shardInfo
	event.DispatchUpdate(ev, "reading all tablets")

	if err := reparentFunctions.PreRecoveryProcesses(ctx); err != nil {
		return err
	}

	return nil
}
