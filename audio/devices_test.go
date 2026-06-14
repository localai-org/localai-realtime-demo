package audio

import "testing"

func TestPickDevice(t *testing.T) {
	names := []string{
		"Wed Camera",
		"HDA Intel PCH",
		"Jabra SPEAK 510 USB",
		"USB Audio",
	}

	tests := []struct {
		name    string
		query   string
		want    int
		wantErr bool
	}{
		{"empty means default", "", -1, false},
		{"unique substring", "jabra", 2, false},
		{"case-insensitive substring", "INTEL", 1, false},
		{"exact match", "USB Audio", 3, false},
		{"exact wins over substring", "Jabra SPEAK 510 USB", 2, false},
		{"no match", "bluetooth", -1, true},
		{"ambiguous substring", "usb", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickDevice(names, tt.query)
			if (err != nil) != tt.wantErr {
				t.Fatalf("pickDevice(%q) err=%v, wantErr=%v", tt.query, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("pickDevice(%q) = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestDeviceNames(t *testing.T) {
	// deviceNames must preserve order and arity so pickDevice indexes line up
	// with the DeviceInfo slice resolveDevices indexes into.
	if got := deviceNames(nil); len(got) != 0 {
		t.Fatalf("deviceNames(nil) = %v, want empty", got)
	}
}
