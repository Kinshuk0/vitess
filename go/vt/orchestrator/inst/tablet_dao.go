/*
Copyright 2020 The Vitess Authors.

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

package inst

import (
	"context"
	"errors"

	"github.com/golang/protobuf/proto"
	"vitess.io/vitess/go/vt/logutil"
	"vitess.io/vitess/go/vt/orchestrator/db"
	"vitess.io/vitess/go/vt/orchestrator/external/golib/log"
	"vitess.io/vitess/go/vt/orchestrator/external/golib/sqlutils"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	"vitess.io/vitess/go/vt/vttablet/tmclient"
)

// ErrTabletAliasNil is a fixed error message.
var ErrTabletAliasNil = errors.New("tablet alias is nil")

// TabletSetMaster designates the tablet that owns an instance as the master.
func TabletSetMaster(instanceKey InstanceKey) error {
	if instanceKey.Hostname == "" {
		return errors.New("can't set tablet to master: instance is unspecified")
	}
	tablet, err := ReadTablet(instanceKey)
	if err != nil {
		return err
	}
	tmc := tmclient.NewTabletManagerClient()
	if err := tmc.ChangeType(context.TODO(), tablet, topodatapb.TabletType_MASTER); err != nil {
		return err
	}
	// Proactively change the tablet type locally so we don't spam this until we get the refresh.
	tablet.Type = topodatapb.TabletType_MASTER
	if err := SaveTablet(tablet); err != nil {
		log.Errore(err)
	}
	return nil
}

// ReadTablet reads the vitess tablet record.
func ReadTablet(instanceKey InstanceKey) (*topodatapb.Tablet, error) {
	query := `
		select
			info
		from
			vitess_tablet
		where hostname=? and port=?
		`
	args := sqlutils.Args(instanceKey.Hostname, instanceKey.Port)
	tablet := &topodatapb.Tablet{}
	err := db.QueryOrchestrator(query, args, func(row sqlutils.RowMap) error {
		return proto.UnmarshalText(row.GetString("info"), tablet)
	})
	if err != nil {
		return nil, err
	}
	if tablet.Alias == nil {
		return nil, ErrTabletAliasNil
	}
	return tablet, nil
}

// SaveTablet saves the tablet record against the instanceKey.
func SaveTablet(tablet *topodatapb.Tablet) error {
	tabletType := int(tablet.Type)
	var timestamp int64
	if tablet.Type == topodatapb.TabletType_MASTER {
		timestamp = logutil.ProtoToTime(tablet.MasterTermStartTime).UnixNano()
	}
	_, err := db.ExecOrchestrator(`
		replace
			into vitess_tablet (
				hostname, port, tablet_type, master_timestamp, info
			) values (
				?, ?, ?, ?, ?
			)
		`,
		tablet.MysqlHostname,
		int(tablet.MysqlPort),
		tabletType,
		timestamp,
		proto.CompactTextString(tablet),
	)
	return err
}
