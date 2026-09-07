package uupdump

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/internal/iotrace"
	"github.com/devcell-sh/go-winkit/media/isokit"
)

type AssembleConfig struct {
	WorkDir string
	ISOPath string
	Label   string
	RefESDs []string // glob patterns for reference ESD files (e.g. "/path/to/*.esd")

	// Logger receives leveled progress output (milestones at Info,
	// detail at Debug). LogFunc is the level-less predecessor, used
	// only when Logger is nil; it receives everything.
	Logger  *slog.Logger
	LogFunc func(format string, args ...any)

	// BootWimCompression and InstallWimCompression select the WIM
	// compression format. The zero value keeps the formats standard
	// Windows media ships with, so leaving them unset changes nothing.
	//
	// Compressing install.wim with LZMS dominates assembly time and is
	// wasted on media a test boots once and discards, which is what these
	// exist for. Measure before switching: a larger image moves cost into
	// ISO mastering and the guest's reads rather than removing it.
	BootWimCompression    Compression
	InstallWimCompression Compression

	// BootOnly skips install.wim creation: the ISO carries boot.wim and
	// the setup media tree only. Use this when only the boot environment
	// is needed (e.g. WinPE testing) and the source ESDs cannot produce
	// a complete install image (the common case for UUP dump builds,
	// whose checkpoint payloads live in cabs the assembler cannot consume).
	BootOnly bool

	// ReuseBootWim skips rebuilding boot.wim when the work directory
	// already holds one. It trades correctness for speed and is only for
	// iterating on the later stages.
	//
	// Off by default, and deliberately so: boot.wim goes stale when the
	// source ESD changes *or when this package changes*, and a silently
	// reused artifact makes a fix to the export path look like it did
	// nothing. Reuse announces itself in the log for the same reason.
	ReuseBootWim bool
}

// Compression selects a WIM compression format.
//
// It exists instead of using wimlib.Compression directly because that
// type's zero value is None: a config field of that type would silently
// produce uncompressed media for every caller that did not set it.
type Compression int

const (
	// CompressionDefault keeps the format standard Windows media uses.
	CompressionDefault Compression = iota
	CompressionNone
	CompressionLZX
	CompressionLZMS
)

func (c Compression) wimlib(fallback wimlib.Compression) wimlib.Compression {
	switch c {
	case CompressionNone:
		return wimlib.None
	case CompressionLZX:
		return wimlib.LZX
	case CompressionLZMS:
		return wimlib.LZMS
	default:
		return fallback
	}
}

// bootCompression is LZX unless overridden: that is what Windows boot
// media ships with.
func (c AssembleConfig) bootCompression() wimlib.Compression {
	return c.BootWimCompression.wimlib(wimlib.LZX)
}

// installCompression is LZMS unless overridden: that is what Windows
// install media ships with.
func (c AssembleConfig) installCompression() wimlib.Compression {
	return c.InstallWimCompression.wimlib(wimlib.LZMS)
}

// canReuseBootWim reports whether an existing boot.wim may stand in for a
// rebuild, and says so in the log when it does.
func (c AssembleConfig) canReuseBootWim(bootWimPath string) bool {
	if !c.ReuseBootWim {
		return false
	}
	info, err := os.Stat(bootWimPath)
	if err != nil || info.Size() == 0 {
		return false
	}
	c.warnf("REUSING existing boot.wim (%.1f MB) — not rebuilt from this ESD "+
		"or this code; clear ReuseBootWim to force a rebuild",
		float64(info.Size())/(1024*1024))
	return true
}

func (c *AssembleConfig) logf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Info(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

func (c *AssembleConfig) debugf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Debug(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

func (c *AssembleConfig) warnf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Warn(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc("WARNING: "+format, args...)
	}
}

func (c *AssembleConfig) errorf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Error(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc("ERROR: "+format, args...)
	}
}

// bootWimSetupFiles lists the sources/ files that must be injected into
// boot.wim image 2 (Windows Setup) so that setup.exe can run inside WinPE.
// Derived from CrystalFetch converter/convert.sh bootSourcesList.
var bootWimSetupFiles = []string{
	"sources/setup.exe",
	"sources/SetupHost.exe",
	"sources/SetupCore.dll",
	"sources/SetupMgr.dll",
	"sources/SetupPlatform.dll",
	"sources/SetupPlatform.exe",
	"sources/SetupPlatform.cfg",
	"sources/SetupPrep.exe",
	"sources/setupcompat.dll",
	"sources/wimgapi.dll",
	"sources/wimprovider.dll",
	"sources/win32ui.dll",
	"sources/w32uiimg.dll",
	"sources/w32uires.dll",
	"sources/uxlib.dll",
	"sources/uxlibres.dll",
	"sources/spwizeng.dll",
	"sources/spwizimg.dll",
	"sources/spwizres.dll",
	"sources/spflvrnt.dll",
	"sources/spprgrss.dll",
	"sources/imagelib.dll",
	"sources/imagingprovider.dll",
	"sources/dism.exe",
	"sources/dismapi.dll",
	"sources/dismcore.dll",
	"sources/dismcoreps.dll",
	"sources/dismprov.dll",
	"sources/bcd.dll",
	"sources/bootsvc.dll",
	"sources/cmisetup.dll",
	"sources/unattend.dll",
	"sources/unbcl.dll",
	"sources/xmllite.dll",
	"sources/lang.ini",
	"sources/locale.nls",
	"sources/compliance.ini",
	"sources/autorun.dll",
	"sources/MediaSetupUIMgr.dll",
	"sources/vhdprovider.dll",
	"sources/ServicingCommon.dll",
	"sources/SmiEngine.dll",
	"sources/wdscore.dll",
	"sources/wdscsl.dll",
	"sources/diagnostic.dll",
	"sources/diager.dll",
	"sources/diagtrack.dll",
	"sources/diagtrackrunner.exe",
	"sources/cryptosetup.dll",
	"sources/input.dll",
	"sources/reagent.dll",
	"sources/reagent.xml",
	"sources/schema.dat",
	"sources/segoeui.ttf",
	"sources/hwcompat.dll",
	"sources/hwcompat.txt",
	"sources/hwreqchk.dll",
	"sources/ndiscompl.dll",
	"sources/pnpibs.dll",
	"sources/offline.xml",
	"sources/appraiser.dll",
	"sources/compatctrl.dll",
	"sources/compatprovider.dll",
	"sources/folderprovider.dll",
	"sources/logprovider.dll",
	"sources/nlsbres.dll",
	"sources/ntdsupg.dll",
	"sources/upgloader.dll",
	"sources/upgrade_frmwrk.xml",
	"sources/sqmapi.dll",
	"sources/utcapi.dll",
	"sources/wpx.dll",
	"sources/WinDlp.dll",
	"sources/ARUNIMG.dll",
	"sources/arunres.dll",
	"sources/alert.gif",
	"sources/warning.gif",
	"sources/winsetup.dll",
	"sources/rollback.exe",
	"sources/appcompat.xsl",
	"sources/appcompat_bidi.xsl",
	"sources/appcompat_detailed_bidi_txt.xsl",
	"sources/appcompat_detailed_txt.xsl",
	"sources/idwbinfo.txt",
	"sources/hwexclude.txt",
	"sources/hwexcludePE.txt",
	"sources/hwcompatPE.txt",
	"sources/testplugin.dll",
	"sources/wdsclient.dll",
	"sources/wdsclientapi.dll",
	"sources/wdscommonlib.dll",
	"sources/wdsimage.dll",
	"sources/wdstptc.dll",
	"sources/wdsutil.dll",
	"sources/reagent.admx",
	"sources/inf/setup.cfg",
}

// fileSize reports a file's size for tracing, or 0 when it cannot be
// read. Tracing must never be the reason a build fails, so errors here are
// deliberately swallowed.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// stagedContentSize totals the extracted setup media in the stage tree,
// excluding the WIMs this package generates into it.
//
// Two simpler measures are both wrong here, because the work directory
// persists between runs. Totalling the tree credits the extraction with
// artifacts earlier runs wrote: it once reported 730.7 MB for an
// extraction of 183.9 MB. Measuring growth across the operation reports
// zero, because re-extracting the same content overwrites in place rather
// than growing the tree. Excluding the generated artifacts is correct
// under both, and does not depend on whether cleanup ran first.
//
// Skipped when tracing is off: walking an extracted Windows image is not
// free and nothing would read the result.
func stagedContentSize(stageDir string) int64 {
	if !iotrace.Default().Enabled() {
		return 0
	}

	generated := map[string]bool{
		filepath.Join(stageDir, "sources", "boot.wim"):    true,
		filepath.Join(stageDir, "sources", "install.wim"): true,
	}

	var total int64
	filepath.Walk(stageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || generated[path] {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// clearGeneratedArtifacts removes the WIMs this run rebuilds, so a failure
// cannot leave a complete-looking artifact behind for the next run (or a
// reader) to mistake for current output.
//
// Only the generated files go: the extracted setup media is the staged
// tree the ISO is mastered from and is expensive to reproduce. boot.wim is
// spared when it is about to be reused, since clearing it would delete the
// very artifact reuse depends on.
func clearGeneratedArtifacts(stageDir string, keepBootWim bool, cfg AssembleConfig) {
	sources := filepath.Join(stageDir, "sources")

	stale := []string{filepath.Join(sources, "install.wim")}
	if !keepBootWim {
		stale = append(stale, filepath.Join(sources, "boot.wim"))
	}

	for _, path := range stale {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			cfg.warnf("could not clear stale %s: %v", filepath.Base(path), err)
			continue
		}
		cfg.debugf("cleared stale %s (%.1f MB) from a previous run",
			filepath.Base(path), float64(info.Size())/(1024*1024))
		iotrace.Record(iotrace.Op{
			Kind: iotrace.KindRemove, Path: path, Bytes: info.Size(),
			Detail: "stale artifact from a previous run",
		})
	}
}

// openESD opens the install ESD and wires up the component ESDs holding
// the blobs its images reference.
//
// It exists as a helper because boot.wim needs two copies of the same
// source image and wimlib will not export one image object into a WIM
// twice, so the Setup copy is taken from a second, independent handle
// that must be set up identically. logDetail keeps the (lengthy)
// reference logging to the first open.
func openESD(esdPath string, cfg AssembleConfig, logDetail bool) (*wimlib.WIM, error) {
	info, err := os.Stat(esdPath)
	if err != nil {
		return nil, fmt.Errorf("stat ESD: %w", err)
	}
	if logDetail {
		cfg.debugf("opening ESD: %s (%.1f MB)", esdPath, float64(info.Size())/(1024*1024))
	}

	done := iotrace.Start(iotrace.KindOpenWIM, esdPath)
	esd, err := wimlib.OpenWIM(esdPath)
	done(info.Size(), err)
	if err != nil {
		return nil, fmt.Errorf("opening ESD: %w", err)
	}

	if err := referenceComponentESDs(esd, cfg, logDetail); err != nil {
		esd.Close()
		return nil, err
	}
	return esd, nil
}

// referenceComponentESDs points esd at the component ESDs that hold the
// blobs its images reference. Without them an export fails: the install
// ESD carries metadata for images whose data lives in sibling files.
func referenceComponentESDs(esd *wimlib.WIM, cfg AssembleConfig, logDetail bool) error {
	if len(cfg.RefESDs) == 0 {
		return nil
	}

	logf := cfg.debugf
	if !logDetail {
		logf = func(string, ...any) {}
	}

	logf("referencing component ESDs via %d glob pattern(s)", len(cfg.RefESDs))
	for _, g := range cfg.RefESDs {
		logf("  glob pattern: %s", g)
		matches, globErr := filepath.Glob(g)
		if globErr != nil {
			logf("  WARNING: Go filepath.Glob error: %v", globErr)
			continue
		}
		logf("  Go filepath.Glob matched %d file(s):", len(matches))
		for _, m := range matches {
			info, _ := os.Stat(m)
			if info != nil {
				logf("    %s (%.1f MB)", filepath.Base(m), float64(info.Size())/(1024*1024))
			} else {
				logf("    %s (stat failed)", filepath.Base(m))
			}
		}
	}

	// Individual file paths first: this bypasses wimlib's own glob
	// expansion, which has resolved to nothing on some platforms.
	var refPaths []string
	for _, g := range cfg.RefESDs {
		dir := filepath.Dir(g)
		entries, err := os.ReadDir(dir)
		if err != nil {
			logf("  WARNING: cannot read dir %s: %v", dir, err)
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".esd") {
				refPaths = append(refPaths, filepath.Join(dir, e.Name()))
			}
		}
	}

	logf("resolved %d individual ESD file(s) for reference", len(refPaths))
	for _, p := range refPaths {
		if info, _ := os.Stat(p); info != nil {
			logf("  ref: %s (%.1f MB)", filepath.Base(p), float64(info.Size())/(1024*1024))
		}
	}

	if len(refPaths) == 0 {
		logf("no individual ESD files found, trying glob reference")
		doneRef := iotrace.StartDetail(iotrace.KindRefISO, "",
			fmt.Sprintf("%d glob pattern(s)", len(cfg.RefESDs)))
		err := esd.ReferenceResourceFiles(cfg.RefESDs)
		doneRef(0, err)
		if err != nil {
			return fmt.Errorf("referencing component ESDs: %w", err)
		}
		return nil
	}

	logf("calling wimlib_reference_resource_files with %d individual paths (no glob)", len(refPaths))
	doneRef := iotrace.StartDetail(iotrace.KindRefISO, "",
		fmt.Sprintf("%d individual path(s)", len(refPaths)))
	err := esd.ReferenceResourceFilePaths(refPaths)
	doneRef(0, err)
	if err == nil {
		logf("individual-path reference succeeded")
		return nil
	}

	logf("WARNING: individual-path reference failed: %v", err)
	logf("falling back to glob-based reference")
	doneGlob := iotrace.StartDetail(iotrace.KindRefISO, "", "glob fallback")
	err = esd.ReferenceResourceFiles(cfg.RefESDs)
	doneGlob(0, err)
	if err != nil {
		return fmt.Errorf("referencing component ESDs: %w", err)
	}
	return nil
}

// buildBootWim produces sources/boot.wim with the two images Windows boot
// media carries: image 1 is WinPE, image 2 is Windows Setup. Both come
// from the same ESD image and differ only in the setup payload injected
// into image 2.
//
// Nearly all of the wall time is the single Write at the end, which
// compresses the whole image; the exports themselves only move references.
func buildBootWim(esd *wimlib.WIM, esdPath, bootWimPath, stageDir string, cfg AssembleConfig) error {
	compression := cfg.bootCompression()

	cfg.debugf("creating boot.wim with 2 images")
	doneCreate := iotrace.StartDetail(iotrace.KindCreateWIM, bootWimPath, "in memory")
	bootWim, err := wimlib.CreateWIM(compression)
	doneCreate(0, err)
	if err != nil {
		return fmt.Errorf("creating boot.wim: %w", err)
	}
	defer bootWim.Close()

	cfg.debugf("exporting WinPE (ESD image 2) → boot.wim image 1")
	doneWinPE := iotrace.StartDetail(iotrace.KindExportImage, bootWimPath,
		"esd image 2 -> boot.wim image 1 (WinPE)")
	err = esd.ExportImage(2, bootWim, compression)
	doneWinPE(0, err)
	if err != nil {
		return fmt.Errorf("exporting WinPE (image 2): %w", err)
	}

	// Renamed before the second copy arrives, not afterwards with the
	// other naming: image names are unique within a WIM, and both copies
	// come off the same source image carrying the same name.
	if err := bootWim.SetImageName(1, "Microsoft Windows PE", "Microsoft Windows PE"); err != nil {
		return fmt.Errorf("naming boot.wim image 1: %w", err)
	}

	// The Setup image is the same source image as the WinPE one, but
	// wimlib refuses to export a given image into a WIM twice
	// (WIMLIB_ERR_DUPLICATE_EXPORTED_IMAGE). It identifies an image by the
	// in-memory metadata object rather than by content, so a second,
	// independent handle on the same file yields a distinct object that
	// exports cleanly alongside the first. The file data is not
	// duplicated: blobs are stored once, deduplicated by hash.
	cfg.debugf("exporting Windows Setup (ESD image 2) → boot.wim image 2")
	esdSetup, err := openESD(esdPath, cfg, false)
	if err != nil {
		return fmt.Errorf("reopening ESD for the Setup image: %w", err)
	}
	defer esdSetup.Close()

	doneSetup := iotrace.StartDetail(iotrace.KindExportImage, bootWimPath,
		"esd image 2 -> boot.wim image 2 (Setup), via a second ESD handle")
	err = esdSetup.ExportImage(2, bootWim, compression)
	doneSetup(0, err)
	if err != nil {
		return fmt.Errorf("exporting Setup (image 2 copy): %w", err)
	}

	// Image 1 was already named above, before the export that would have
	// collided with it.
	if err := bootWim.SetImageName(2, "Microsoft Windows Setup", "Microsoft Windows Setup"); err != nil {
		cfg.warnf("failed to set image 2 name: %v", err)
	}

	// Set FLAGS on images (9 = WinPE, 2 = Setup) and mark image 2 as boot.
	if err := bootWim.SetImageProperty(1, "FLAGS", "9"); err != nil {
		cfg.warnf("failed to set FLAGS=9 on image 1: %v", err)
	}
	if err := bootWim.SetImageProperty(2, "FLAGS", "2"); err != nil {
		cfg.warnf("failed to set FLAGS=2 on image 2: %v", err)
	}
	if err := bootWim.SetBootImage(2); err != nil {
		return fmt.Errorf("setting boot image to 2: %w", err)
	}

	cfg.debugf("writing boot.wim → %s", bootWimPath)
	doneBootWrite := iotrace.StartDetail(iotrace.KindWriteWIM, bootWimPath, "2 images")
	err = bootWim.Write(bootWimPath)
	doneBootWrite(fileSize(bootWimPath), err)
	if err != nil {
		return fmt.Errorf("writing boot.wim: %w", err)
	}

	if info, _ := os.Stat(bootWimPath); info != nil {
		cfg.debugf("boot.wim size: %.1f MB", float64(info.Size())/(1024*1024))
	}

	return injectSetupIntoBootWim(bootWimPath, stageDir, cfg)
}

// injectSetupIntoBootWim turns boot.wim image 2 into Windows Setup: it
// drops winpeshl.ini so WinRE recovery does not launch, and adds setup.exe
// plus the sources/ files setup.exe depends on.
//
// This runs against boot.wim on disk rather than the in-memory WIM the
// exports built. A freshly exported image shares its metadata with the
// source WIM it came from, and wimlib refuses to modify an image that is
// referenced from more than one place
// (WIMLIB_ERR_IMAGE_HAS_MULTIPLE_REFERENCES). Writing and reopening gives
// image 2 metadata of its own, which is then modifiable in place.
func injectSetupIntoBootWim(bootWimPath, stageDir string, cfg AssembleConfig) error {
	doneOpen := iotrace.Start(iotrace.KindOpenWIM, bootWimPath)
	bootWim, err := wimlib.OpenWIM(bootWimPath)
	doneOpen(fileSize(bootWimPath), err)
	if err != nil {
		return fmt.Errorf("reopening boot.wim to inject Setup: %w", err)
	}
	defer bootWim.Close()

	cfg.debugf("removing winpeshl.ini from boot.wim image 2 (prevents WinRE recovery launch)")
	if err := bootWim.UpdateImageDelete(2, "/Windows/System32/winpeshl.ini"); err != nil {
		cfg.warnf("failed to delete winpeshl.ini from image 2: %v (may not exist)", err)
	}

	// setup.exe at the root is the entry point; the sources/ files below
	// are its dependencies.
	setupExe := filepath.Join(stageDir, "setup.exe")
	if _, err := os.Stat(setupExe); err == nil {
		cfg.debugf("injecting setup.exe → boot.wim image 2")
		if err := bootWim.UpdateImageAdd(2, setupExe, "/setup.exe"); err != nil {
			return fmt.Errorf("injecting setup.exe into boot.wim: %w", err)
		}
	} else {
		cfg.warnf("setup.exe not found in staging dir — boot.wim image 2 may not boot correctly")
	}

	injected := 0
	var injectedBytes int64
	for _, relPath := range bootWimSetupFiles {
		srcPath := filepath.Join(stageDir, relPath)
		info, err := os.Stat(srcPath)
		if err != nil {
			continue
		}
		wimPath := "/" + relPath
		if err := bootWim.UpdateImageAdd(2, srcPath, wimPath); err != nil {
			cfg.warnf("failed to inject %s into boot.wim: %v", relPath, err)
			iotrace.Record(iotrace.Op{Kind: iotrace.KindUpdateWIM, Path: wimPath,
				Detail: "boot.wim image 2", Err: err})
			continue
		}
		injected++
		injectedBytes += info.Size()
	}
	cfg.debugf("injected %d setup files into boot.wim image 2", injected)
	iotrace.Record(iotrace.Op{
		Kind:   iotrace.KindUpdateWIM,
		Path:   bootWimPath,
		Bytes:  injectedBytes,
		Detail: fmt.Sprintf("injected %d setup files into image 2", injected),
	})

	doneWrite := iotrace.StartDetail(iotrace.KindWriteWIM, bootWimPath,
		"overwrite with Setup payload")
	err = bootWim.Overwrite()
	doneWrite(fileSize(bootWimPath), err)
	if err != nil {
		return fmt.Errorf("committing Setup payload to boot.wim: %w", err)
	}
	return nil
}

// AssembleISO converts an ESD file into a bootable Windows installer ISO.
//
// UUP dump ESDs typically contain 3 images:
//
//	image 1: "Windows Setup Media"   → extract to staging dir (boot files)
//	image 2: "WinRE / WinPE"         → export to boot.wim (boot environment)
//	image 3: "Windows 11 Pro" (etc.) → export to install.wim (OS image)
//
// boot.wim gets two images (matching CrystalFetch converter):
//
//	image 1: "Microsoft Windows PE"     — WinPE recovery environment
//	image 2: "Microsoft Windows Setup"  — installer with setup.exe (bootable)
//
// Traditional install ESDs may have 4+ images (images 2-3 boot, 4+ editions).
func AssembleISO(_ context.Context, esdPath string, cfg AssembleConfig) error {
	if cfg.Label == "" {
		cfg.Label = "YOURISO"
	}

	cfg.logf("assembling ISO")
	cfg.debugf("install ESD: %s", esdPath)

	stageDir := filepath.Join(cfg.WorkDir, "iso-stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("creating stage dir: %w", err)
	}

	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available: build with -tags wimlib and wimlib installed (brew install wimlib)")
	}

	esd, err := openESD(esdPath, cfg, true)
	if err != nil {
		return err
	}
	defer esd.Close()

	imageCount, err := esd.ImageCount()
	if err != nil {
		return fmt.Errorf("counting ESD images: %w", err)
	}
	cfg.debugf("ESD contains %d image(s)", imageCount)
	for i := 1; i <= imageCount; i++ {
		desc, _ := esd.ImageDescription(i)
		cfg.debugf("  image %d: %s", i, desc)
	}

	if imageCount < 3 {
		return fmt.Errorf("ESD has %d image(s), expected at least 3 (setup media, boot env, OS)", imageCount)
	}

	// Decided before anything is cleared or measured: reuse inspects the
	// previous run's boot.wim, and clearing has to happen before the
	// extract so its growth measurement reflects this run alone.
	reuseBootWim := cfg.canReuseBootWim(filepath.Join(stageDir, "sources", "boot.wim"))
	clearGeneratedArtifacts(stageDir, reuseBootWim, cfg)

	cfg.logf("extracting setup media")
	cfg.debugf("stage dir: %s", stageDir)
	doneExtract := iotrace.StartDetail(iotrace.KindExtract, stageDir,
		"esd image 1 (setup media)")
	err = esd.ExtractImage(1, stageDir, nil)
	doneExtract(stagedContentSize(stageDir), err)
	if err != nil {
		return fmt.Errorf("extracting setup media (image 1): %w", err)
	}
	cfg.debugf("image 1 extraction complete")

	sourcesDir := filepath.Join(stageDir, "sources")
	if err := os.MkdirAll(sourcesDir, 0o755); err != nil {
		return fmt.Errorf("creating sources dir: %w", err)
	}

	bootWimPath := filepath.Join(sourcesDir, "boot.wim")
	installWimPath := filepath.Join(sourcesDir, "install.wim")

	if reuseBootWim {
		cfg.logf("skipping boot.wim build (reusing the existing artifact)")
	} else if err := buildBootWim(esd, esdPath, bootWimPath, stageDir, cfg); err != nil {
		return err
	}

	if cfg.BootOnly {
		cfg.logf("boot-only mode: skipping install.wim (source ESDs lack complete install-image payload)")
	} else {
		// install.wim: contains the OS image(s) — image 3+ from the ESD
		cfg.logf("creating install.wim (%d image(s))", imageCount-2)
		installWim, err := wimlib.CreateWIM(cfg.installCompression())
		if err != nil {
			return fmt.Errorf("creating install.wim: %w", err)
		}
		defer installWim.Close()

		for i := 3; i <= imageCount; i++ {
			desc, _ := esd.ImageDescription(i)
			if desc == "" {
				desc = fmt.Sprintf("image %d", i)
			}
			cfg.logf("exporting %s (image %d/%d) → install.wim", desc, i, imageCount)
			doneExport := iotrace.StartDetail(iotrace.KindExportImage, installWimPath,
				fmt.Sprintf("esd image %d -> install.wim (%s)", i, desc))
			err := esd.ExportImage(i, installWim, cfg.installCompression())
			doneExport(0, err)
			if err != nil {
				cfg.errorf("wimlib_export_image(%d) failed: %v", i, err)
				cfg.debugf("  The UUP dump component ESDs are missing blobs the install image references.")
				cfg.debugf("  This is expected: Windows externalizes install-image payload into checkpoint")
				cfg.debugf("  MSUs and FoD cabs that the ESD-only assembler cannot consume.")
				cfg.debugf("  For complete install media, use the mct source (winkit fetch --source mct).")
				cfg.debugf("  For boot media only (WinPE), use winkit build which uses boot-only mode.")
				cfg.debugf("  The install ESD: %s", esdPath)
				return fmt.Errorf("exporting image %d (%s): %w", i, desc, err)
			}
			cfg.debugf("image %d export complete", i)
		}

		cfg.logf("writing install.wim")
		cfg.debugf("install.wim path: %s", installWimPath)
		doneInstallWrite := iotrace.StartDetail(iotrace.KindWriteWIM, installWimPath,
			fmt.Sprintf("%d image(s)", imageCount-2))
		err = installWim.Write(installWimPath)
		doneInstallWrite(fileSize(installWimPath), err)
		if err != nil {
			return fmt.Errorf("writing install.wim: %w", err)
		}

		installInfo, _ := os.Stat(installWimPath)
		if installInfo != nil {
			cfg.debugf("install.wim size: %.1f MB", float64(installInfo.Size())/(1024*1024))
		}
	}

	cfg.logf("mastering ISO")
	cfg.debugf("ISO path: %s", cfg.ISOPath)
	doneISO := iotrace.StartDetail(iotrace.KindCreateISO, cfg.ISOPath,
		"from "+stageDir+", label "+cfg.Label)
	err = isokit.CreateWindowsISO(cfg.ISOPath, stageDir, cfg.Label)
	doneISO(fileSize(cfg.ISOPath), err)
	if err != nil {
		return fmt.Errorf("creating ISO: %w", err)
	}

	isoInfo, _ := os.Stat(cfg.ISOPath)
	if isoInfo != nil {
		cfg.logf("ISO created (%.1f MB)", float64(isoInfo.Size())/(1024*1024))
		cfg.debugf("ISO: %s", cfg.ISOPath)
	}
	return nil
}
