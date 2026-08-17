package dbus

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestAddressParsing(t *testing.T) {
	tests := []struct {
		name    string
		envAddr string
		envXDG  string
		wantErr error
	}{
		{
			name:    "unix path",
			envAddr: "unix:path=/run/user/1000/bus",
		},
		{
			name:    "unix abstract",
			envAddr: "unix:abstract=/tmp/dbus-Ab",
		},
		{
			name:    "unix path with guid",
			envAddr: "unix:path=/x,guid=deadbeef",
		},
		{
			name:    "unix path percent encoded",
			envAddr: "unix:path=%2frun%2fbus",
		},
		{
			name:    "tcp transport unsupported",
			envAddr: "tcp:host=localhost,port=1",
			wantErr: errors.New("dbus: invalid bus address (invalid or unsupported transport)"),
		},
		{
			name:    "empty env without XDG_RUNTIME_DIR",
			envAddr: "",
			envXDG:  "",
			wantErr: ErrNoSessionBus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
			os.Unsetenv("XDG_RUNTIME_DIR")

			if tt.envAddr != "" {
				os.Setenv("DBUS_SESSION_BUS_ADDRESS", tt.envAddr)
			}
			if tt.envXDG != "" {
				os.Setenv("XDG_RUNTIME_DIR", tt.envXDG)
			}

			addr, err := getSessionBusAddress()
			if tt.envAddr == "" && tt.envXDG == "" {
				if err != tt.wantErr {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error getting session bus address: %v", err)
			}

			closer, err := dialAddress(addr)
			if tt.wantErr != nil {
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if closer != nil && !reflect.ValueOf(closer).IsNil() {
				closer.Close()
			}
		})
	}
}
