// Package platform detects the host operating system, init system,
// package manager, and firewall tooling to adapt deployment commands.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OS represents a detected operating system family.
type OS int

const (
	Unknown OS = iota
	Ubuntu
	Debian
	RHEL // CentOS, Fedora, Rocky, Alma
	Arch
	Alpine
	FreeBSD
	MacOS
	Windows
)

func (o OS) String() string {
	switch o {
	case Ubuntu:
		return "Ubuntu"
	case Debian:
		return "Debian"
	case RHEL:
		return "RHEL/CentOS/Fedora"
	case Arch:
		return "Arch Linux"
	case Alpine:
		return "Alpine"
	case FreeBSD:
		return "FreeBSD"
	case MacOS:
		return "macOS"
	case Windows:
		return "Windows"
	default:
		return "Unknown"
	}
}

// InitSystem represents the init/service manager in use.
type InitSystem int

const (
	InitUnknown InitSystem = iota
	Systemd
	OpenRC
	Launchd
	WindowsService
	RcD // FreeBSD rc.d
)

func (i InitSystem) String() string {
	switch i {
	case Systemd:
		return "systemd"
	case OpenRC:
		return "OpenRC"
	case Launchd:
		return "launchd"
	case WindowsService:
		return "Windows Service"
	case RcD:
		return "rc.d"
	default:
		return "unknown"
	}
}

// PackageManager represents the system package manager.
type PackageManager int

const (
	PkgUnknown PackageManager = iota
	Apt
	Yum
	Dnf
	Pacman
	Apk
	Brew
	Choco
	PkgFreeBSD
)

func (p PackageManager) String() string {
	switch p {
	case Apt:
		return "apt"
	case Yum:
		return "yum"
	case Dnf:
		return "dnf"
	case Pacman:
		return "pacman"
	case Apk:
		return "apk"
	case Brew:
		return "brew"
	case Choco:
		return "choco"
	case PkgFreeBSD:
		return "pkg"
	default:
		return "unknown"
	}
}

// FirewallTool represents the firewall management tool available.
type FirewallTool int

const (
	FwUnknown FirewallTool = iota
	UFW
	Firewalld
	Iptables
	Nftables
	PF        // macOS/FreeBSD
	WinFW     // Windows Firewall
)

func (f FirewallTool) String() string {
	switch f {
	case UFW:
		return "ufw"
	case Firewalld:
		return "firewalld"
	case Iptables:
		return "iptables"
	case Nftables:
		return "nftables"
	case PF:
		return "pf"
	case WinFW:
		return "Windows Firewall"
	default:
		return "unknown"
	}
}

// Info holds all detected platform information.
type Info struct {
	OS             OS
	OSVersion      string
	Arch           string
	InitSystem     InitSystem
	PackageManager PackageManager
	Firewall       FirewallTool
	IsRoot         bool
	HasDocker      bool
	HasOpenSSL     bool
	HasStepCLI     bool
	GoOS           string
	GoArch         string
}

// Detect probes the current system and returns platform information.
func Detect() Info {
	info := Info{
		GoOS:   runtime.GOOS,
		GoArch: runtime.GOARCH,
		Arch:   runtime.GOARCH,
	}

	switch runtime.GOOS {
	case "linux":
		info.OS, info.OSVersion = detectLinuxDistro()
		info.InitSystem = detectLinuxInit()
		info.PackageManager = detectLinuxPkgManager()
		info.Firewall = detectLinuxFirewall()
		info.IsRoot = os.Geteuid() == 0
	case "darwin":
		info.OS = MacOS
		info.OSVersion = runCmd("sw_vers", "-productVersion")
		info.InitSystem = Launchd
		info.PackageManager = Brew
		info.Firewall = PF
		info.IsRoot = os.Geteuid() == 0
	case "freebsd":
		info.OS = FreeBSD
		info.OSVersion = runCmd("freebsd-version")
		info.InitSystem = RcD
		info.PackageManager = PkgFreeBSD
		info.Firewall = PF
		info.IsRoot = os.Geteuid() == 0
	case "windows":
		info.OS = Windows
		info.InitSystem = WindowsService
		info.PackageManager = Choco
		info.Firewall = WinFW
		info.IsRoot = false // Windows uses UAC, not uid
	}

	info.HasDocker = commandExists("docker")
	info.HasOpenSSL = commandExists("openssl")
	info.HasStepCLI = commandExists("step")

	return info
}

// ServiceInstallCmd returns the command to install a systemd/launchd/rc.d service.
func (i Info) ServiceInstallCmd(name, binaryPath, configPath string) []string {
	switch i.InitSystem {
	case Systemd:
		return []string{
			fmt.Sprintf("sudo cp %s /usr/local/bin/", binaryPath),
			fmt.Sprintf("sudo systemctl daemon-reload"),
			fmt.Sprintf("sudo systemctl enable %s", name),
			fmt.Sprintf("sudo systemctl start %s", name),
		}
	case Launchd:
		return []string{
			fmt.Sprintf("sudo cp %s /usr/local/bin/", binaryPath),
			fmt.Sprintf("sudo launchctl load /Library/LaunchDaemons/io.atlax.%s.plist", name),
		}
	case RcD:
		return []string{
			fmt.Sprintf("sudo cp %s /usr/local/bin/", binaryPath),
			fmt.Sprintf("sudo sysrc %s_enable=YES", name),
			fmt.Sprintf("sudo service %s start", name),
		}
	default:
		return []string{fmt.Sprintf("# Manual installation required for %s", i.InitSystem)}
	}
}

// ServiceFilePath returns the path where the service definition file should go.
func (i Info) ServiceFilePath(name string) string {
	switch i.InitSystem {
	case Systemd:
		return fmt.Sprintf("/etc/systemd/system/%s.service", name)
	case Launchd:
		return fmt.Sprintf("/Library/LaunchDaemons/io.atlax.%s.plist", name)
	case RcD:
		return fmt.Sprintf("/usr/local/etc/rc.d/%s", name)
	default:
		return ""
	}
}

// InstallPackageCmd returns the command to install a package.
func (i Info) InstallPackageCmd(pkg string) string {
	switch i.PackageManager {
	case Apt:
		return fmt.Sprintf("sudo apt-get install -y %s", pkg)
	case Yum:
		return fmt.Sprintf("sudo yum install -y %s", pkg)
	case Dnf:
		return fmt.Sprintf("sudo dnf install -y %s", pkg)
	case Pacman:
		return fmt.Sprintf("sudo pacman -S --noconfirm %s", pkg)
	case Apk:
		return fmt.Sprintf("sudo apk add %s", pkg)
	case Brew:
		return fmt.Sprintf("brew install %s", pkg)
	case Choco:
		return fmt.Sprintf("choco install -y %s", pkg)
	case PkgFreeBSD:
		return fmt.Sprintf("sudo pkg install -y %s", pkg)
	default:
		return fmt.Sprintf("# Install %s manually", pkg)
	}
}

// ConfigBasePath returns the base directory for atlax config files.
func (i Info) ConfigBasePath() string {
	switch i.OS {
	case Windows:
		return `C:\ProgramData\atlax`
	case MacOS:
		return "/usr/local/etc/atlax"
	default:
		return "/etc/atlax"
	}
}

// BinaryInstallPath returns the directory for atlax binaries.
func (i Info) BinaryInstallPath() string {
	switch i.OS {
	case Windows:
		return `C:\Program Files\atlax`
	case MacOS:
		return "/usr/local/bin"
	default:
		return "/usr/local/bin"
	}
}

// LogBasePath returns the base directory for atlax logs.
func (i Info) LogBasePath() string {
	switch i.OS {
	case Windows:
		return `C:\ProgramData\atlax\logs`
	case MacOS:
		return "/usr/local/var/log/atlax"
	default:
		return "/var/log/atlax"
	}
}

// --- internal helpers ---

func detectLinuxDistro() (OS, string) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return Unknown, ""
	}
	content := string(data)

	var id, versionID string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			versionID = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}

	switch id {
	case "ubuntu":
		return Ubuntu, versionID
	case "debian":
		return Debian, versionID
	case "centos", "rhel", "fedora", "rocky", "almalinux":
		return RHEL, versionID
	case "arch", "manjaro", "endeavouros":
		return Arch, versionID
	case "alpine":
		return Alpine, versionID
	default:
		return Unknown, versionID
	}
}

func detectLinuxInit() InitSystem {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return Systemd
	}
	if commandExists("openrc") {
		return OpenRC
	}
	return InitUnknown
}

func detectLinuxPkgManager() PackageManager {
	if commandExists("apt-get") {
		return Apt
	}
	if commandExists("dnf") {
		return Dnf
	}
	if commandExists("yum") {
		return Yum
	}
	if commandExists("pacman") {
		return Pacman
	}
	if commandExists("apk") {
		return Apk
	}
	return PkgUnknown
}

func detectLinuxFirewall() FirewallTool {
	if commandExists("ufw") {
		return UFW
	}
	if commandExists("firewall-cmd") {
		return Firewalld
	}
	if commandExists("nft") {
		return Nftables
	}
	if commandExists("iptables") {
		return Iptables
	}
	return FwUnknown
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCmd(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
