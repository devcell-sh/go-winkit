package imageformat

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/uuid"
)

type Format int

const (
	Qcow2 Format = iota + 1
	UTM
)

func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "qcow2":
		return Qcow2, nil
	case "utm":
		return UTM, nil
	default:
		return 0, fmt.Errorf("unknown image format %q (want qcow2 or utm)", s)
	}
}

func (f Format) Ext() string {
	switch f {
	case Qcow2:
		return ".qcow2"
	case UTM:
		return ".utm"
	default:
		return ""
	}
}

func (f Format) String() string {
	switch f {
	case Qcow2:
		return "qcow2"
	case UTM:
		return "utm"
	default:
		return "unknown"
	}
}

func DefaultOutputName(stage string, f Format) string {
	return "winkit-" + stage + f.Ext()
}

type PackageOpts struct {
	VarsPath string
	VMName   string
	MemoryMB int
	CPUs     int
}

func Package(f Format, qcow2Path, dest string, opts *PackageOpts) error {
	switch f {
	case Qcow2:
		return packageQcow2(qcow2Path, dest)
	case UTM:
		return packageUTM(qcow2Path, dest, opts)
	default:
		return fmt.Errorf("unsupported format for packaging: %v", f)
	}
}

func packageQcow2(src, dest string) error {
	absSrc, _ := filepath.Abs(src)
	absDest, _ := filepath.Abs(dest)
	if absSrc == absDest {
		return nil
	}
	return copyFile(src, dest)
}

func packageUTM(qcow2Path, dest string, opts *PackageOpts) error {
	if opts == nil {
		opts = &PackageOpts{}
	}
	if opts.VMName == "" {
		opts.VMName = "Windows"
	}
	if opts.MemoryMB == 0 {
		opts.MemoryMB = 8192
	}
	if opts.CPUs == 0 {
		opts.CPUs = 4
	}

	if err := os.MkdirAll(filepath.Join(dest, "Data"), 0o755); err != nil {
		return fmt.Errorf("creating UTM bundle: %w", err)
	}

	diskUUID := uuid.New().String()
	diskUUIDUpper := strings.ToUpper(diskUUID)
	diskFilename := diskUUIDUpper + ".qcow2"

	if err := copyFile(qcow2Path, filepath.Join(dest, "Data", diskFilename)); err != nil {
		return fmt.Errorf("copying disk to UTM bundle: %w", err)
	}

	if opts.VarsPath != "" {
		if err := copyFile(opts.VarsPath, filepath.Join(dest, "Data", "efi_vars.fd")); err != nil {
			return fmt.Errorf("copying vars to UTM bundle: %w", err)
		}
	}

	plistData := utmPlistData{
		VMName:       opts.VMName,
		VMUUID:       strings.ToUpper(uuid.New().String()),
		DiskUUID:     diskUUIDUpper,
		DiskFilename: diskFilename,
		MemoryMB:     opts.MemoryMB,
		CPUs:         opts.CPUs,
	}
	f, err := os.Create(filepath.Join(dest, "config.plist"))
	if err != nil {
		return fmt.Errorf("creating config.plist: %w", err)
	}
	defer f.Close()
	return utmPlistTmpl.Execute(f, plistData)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

type utmPlistData struct {
	VMName       string
	VMUUID       string
	DiskUUID     string
	DiskFilename string
	MemoryMB     int
	CPUs         int
}

var utmPlistTmpl = template.Must(template.New("utm").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Backend</key>
	<string>QEMU</string>
	<key>ConfigurationVersion</key>
	<integer>4</integer>
	<key>Display</key>
	<array>
		<dict>
			<key>DownscalingFilter</key>
			<string>Linear</string>
			<key>DynamicResolution</key>
			<true/>
			<key>Hardware</key>
			<string>virtio-ramfb-gl</string>
			<key>NativeResolution</key>
			<false/>
			<key>UpscalingFilter</key>
			<string>Nearest</string>
		</dict>
	</array>
	<key>Drive</key>
	<array>
		<dict>
			<key>Identifier</key>
			<string>{{.DiskUUID}}</string>
			<key>ImageName</key>
			<string>{{.DiskFilename}}</string>
			<key>ImageType</key>
			<string>Disk</string>
			<key>Interface</key>
			<string>NVMe</string>
			<key>InterfaceVersion</key>
			<integer>1</integer>
			<key>ReadOnly</key>
			<false/>
		</dict>
	</array>
	<key>Information</key>
	<dict>
		<key>Icon</key>
		<string>windows</string>
		<key>IconCustom</key>
		<false/>
		<key>Name</key>
		<string>{{.VMName}}</string>
		<key>UUID</key>
		<string>{{.VMUUID}}</string>
	</dict>
	<key>Input</key>
	<dict>
		<key>MaximumUsbShare</key>
		<integer>3</integer>
		<key>UsbBusSupport</key>
		<string>3.0</string>
		<key>UsbSharing</key>
		<false/>
	</dict>
	<key>Network</key>
	<array>
		<dict>
			<key>Hardware</key>
			<string>virtio-net-pci</string>
			<key>IsolateFromHost</key>
			<false/>
			<key>Mode</key>
			<string>Shared</string>
			<key>PortForward</key>
			<array/>
		</dict>
	</array>
	<key>QEMU</key>
	<dict>
		<key>AdditionalArguments</key>
		<array/>
		<key>BalloonDevice</key>
		<false/>
		<key>DebugLog</key>
		<false/>
		<key>Hypervisor</key>
		<true/>
		<key>PS2Controller</key>
		<false/>
		<key>RNGDevice</key>
		<true/>
		<key>RTCLocalTime</key>
		<true/>
		<key>TPMDevice</key>
		<true/>
		<key>TSO</key>
		<false/>
		<key>UEFIBoot</key>
		<true/>
	</dict>
	<key>Serial</key>
	<array/>
	<key>Sharing</key>
	<dict>
		<key>ClipboardSharing</key>
		<true/>
		<key>DirectoryShareMode</key>
		<string>WebDAV</string>
		<key>DirectoryShareReadOnly</key>
		<false/>
	</dict>
	<key>Sound</key>
	<array>
		<dict>
			<key>Hardware</key>
			<string>intel-hda</string>
		</dict>
	</array>
	<key>System</key>
	<dict>
		<key>Architecture</key>
		<string>aarch64</string>
		<key>CPU</key>
		<string>default</string>
		<key>CPUCount</key>
		<integer>{{.CPUs}}</integer>
		<key>CPUFlagsAdd</key>
		<array/>
		<key>CPUFlagsRemove</key>
		<array/>
		<key>ForceMulticore</key>
		<false/>
		<key>JITCacheSize</key>
		<integer>0</integer>
		<key>MemorySize</key>
		<integer>{{.MemoryMB}}</integer>
		<key>Target</key>
		<string>virt</string>
	</dict>
</dict>
</plist>
`))
