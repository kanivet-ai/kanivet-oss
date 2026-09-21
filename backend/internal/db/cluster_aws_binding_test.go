package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newBindingTestDB(t *testing.T) *DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&ClusterAWSBinding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &DB{gdb}
}

func TestClusterAWSBindingRoundTrip(t *testing.T) {
	d := newBindingTestDB(t)
	const cluster = "arn:aws:eks:eu-north-1:243517631187:cluster/sbx"

	if got, err := d.GetClusterAWSBinding(cluster); err != nil || got != nil {
		t.Fatalf("unbound cluster: got %+v, err %v; want nil, nil", got, err)
	}

	first := &ClusterAWSBinding{Cluster: cluster, StartURL: "https://d-1.awsapps.com/start", AccountID: "243517631187", RoleName: "ReadOnlyAccess", Profile: "kanivet-sso-243517631187-ReadOnlyAccess"}
	if err := d.SaveClusterAWSBinding(first); err != nil {
		t.Fatal(err)
	}
	// Switching role replaces the row rather than adding a second one.
	second := &ClusterAWSBinding{Cluster: cluster, StartURL: "https://d-1.awsapps.com/start", AccountID: "243517631187", RoleName: "AdministratorAccess", Profile: "kanivet-sso-243517631187-AdministratorAccess"}
	if err := d.SaveClusterAWSBinding(second); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetClusterAWSBinding(cluster)
	if err != nil || got == nil {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if got.RoleName != "AdministratorAccess" || got.Profile != "kanivet-sso-243517631187-AdministratorAccess" {
		t.Errorf("binding = %+v", got)
	}
	all, err := d.GetClusterAWSBindings()
	if err != nil || len(all) != 1 {
		t.Fatalf("bindings = %+v, err %v; want exactly one", all, err)
	}

	if err := d.DeleteClusterAWSBinding(cluster); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetClusterAWSBinding(cluster); got != nil {
		t.Errorf("binding survived delete: %+v", got)
	}
}
