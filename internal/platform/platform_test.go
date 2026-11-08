package platform

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestParseDoctorFamilies(t *testing.T) {
	debian := []byte(`{"status":"success","data":{"supported":true,"profile":{"family":"debian","support_level":"official","distro_id":"ubuntu"}}}`)
	got, err := ParseDoctor(debian)
	if err != nil || !got.Supported || got.Family != "debian" || got.DistroID != "ubuntu" {
		t.Fatalf("debian: %+v %v", got, err)
	}
	rhel := []byte(`{"status":"success","data":{"supported":true,"profile":{"family":"rhel","support_level":"official","distro_id":"rocky"}}}`)
	got, err = ParseDoctor(rhel)
	if err != nil || !got.Supported || got.Family != "rhel" || got.DistroID != "rocky" {
		t.Fatalf("rhel: %+v %v", got, err)
	}
	fedora := []byte(`{"status":"success","data":{"supported":false,"support_note":"Fedora is not in the Abstrax support matrix","profile":{"family":"fedora","support_level":"unsupported","distro_id":"fedora"}}}`)
	got, err = ParseDoctor(fedora)
	if err != nil || got.Supported || got.Family != "fedora" {
		t.Fatalf("fedora: %+v %v", got, err)
	}
}

func TestCheckRequiresLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this assertion is for non-Linux development hosts")
	}
	_, err := Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("Check = %v", err)
	}
}
