package efiboot

import "testing"

const withStick = "BootCurrent: 0001\n" +
	"Timeout: 1 seconds\n" +
	"BootOrder: 0001,0002\n" +
	"Boot0001* Ubuntu\tHD(1,GPT,5c1c,0x800,0x219800)/File(\\EFI\\ubuntu\\shimx64.efi)\n" +
	"Boot0002* UEFI: SanDisk 1.00, Partition 2\t" +
	"PciRoot(0x0)/Pci(0x8,0x1)/Pci(0x0,0x4)/USB(2,0)/HD(2,GPT,9e0c,0x5f8,0x2800)..BO\n"

func TestParse(t *testing.T) {
	got := Parse(withStick)
	if len(got) != 2 {
		t.Fatalf("Parse found %d entries, want 2: %+v", len(got), got)
	}
	want := Entry{Num: "0001", Label: "Ubuntu", Active: true,
		Path: `HD(1,GPT,5c1c,0x800,0x219800)/File(\EFI\ubuntu\shimx64.efi)`}
	if got[0] != want {
		t.Errorf("first entry = %+v, want %+v", got[0], want)
	}
}

func TestUSB(t *testing.T) {
	cases := []struct {
		name string
		out  string
		num  string
	}{
		{"usb device path", withStick, "0002"},
		{"label only", "Boot0001* Ubuntu\nBoot0003* UEFI:Removable Device\n", "0003"},
		{"inactive", "Boot0001* Ubuntu\nBoot0002  UEFI: USB stick\n", ""},
		{"no stick", "BootCurrent: 0001\nBoot0001* Ubuntu\tHD(1)/File(x)\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := USB(Parse(tc.out))
			if ok != (tc.num != "") || e.Num != tc.num {
				t.Errorf("USB = %q, %v; want %q", e.Num, ok, tc.num)
			}
		})
	}
}
