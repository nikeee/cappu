package install

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lockfile"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// How many jars to download/verify at once. Bounded so a large tree does not
// open hundreds of sockets; the network, not the CPU, is the limit here. Port
// of src/install.ts DOWNLOAD_CONCURRENCY.
const downloadConcurrency = 12

// Options tunes a run. OnProgress is called per materialized package; OnResolve
// per package while resolving (no lockfile). UpdateLock forces a re-resolve and
// lockfile rewrite (used by `cappu add`).
type Options struct {
	UpdateLock bool
	OnProgress func(done, total int, current packages.CoordinateString)
	OnResolve  func(current packages.CoordinateString)
}

// Result is the print-free outcome of installDependencies; the CLI renders it.
type Result struct {
	Installed           []string
	InstalledByCategory Categories
	NoArtifact          []string
	Resolution          packages.Resolution
	TargetDir           string
	FromLock            bool
	LockStale           bool
	IntegrityFailures   []string
	FromStore           []string
}

// Categories splits the written jar paths by configuration.
type Categories struct {
	Compile   []string
	Processor []string
	Test      []string
}

// pending is a package queued for download (sha256/licenses known when locked).
type pending struct {
	coordinates packages.Coordinates
	source      string
	sha256      lockfile.Sha256 // empty when not locked
	hasSha      bool
	licenses    []packages.License
}

type outcome struct {
	locked     *lockfile.LockedPackage
	installed  string
	noArtifact string
	integrity  string
	fromStore  string
}

// Dependencies resolves and downloads the cappu.json dependencies. An existing
// lock is installed exactly as written (every download verified against its
// locked hash); resolution runs only to bootstrap a missing lock, or when
// UpdateLock asks for a rewrite. Port of installDependencies.
func Dependencies(cfg *config.Config, srcs []packages.PackageSource, opts Options) (Result, error) {
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	var lock *lockfile.Lockfile
	if !opts.UpdateLock {
		lock = lockfile.Read(cfg)
	}
	fromLock := lock != nil
	lockStale := lock != nil && !lock.Matches(cfg)

	var resolution packages.Resolution
	var toInstall, processorInstall, testInstall []pending
	if lock != nil {
		toInstall = fromLocked(lock.Packages)
		processorInstall = fromLocked(lock.ProcessorPackages)
		testInstall = fromLocked(lock.TestPackages)
	} else {
		onResolve := func(c packages.Coordinates) {
			if opts.OnResolve != nil {
				opts.OnResolve(c.String())
			}
		}
		main, err := packages.ResolveTransitive(sources.CompileRoots(cfg), srcs, onResolve)
		if err != nil {
			return Result{}, err
		}
		processors, err := resolveIfAny(sources.ProcessorRoots(cfg), srcs, onResolve)
		if err != nil {
			return Result{}, err
		}
		tests, err := resolveIfAny(sources.TestRoots(cfg), srcs, onResolve)
		if err != nil {
			return Result{}, err
		}
		resolution = packages.Resolution{
			Packages:  concat(main.Packages, processors.Packages, tests.Packages),
			Conflicts: concat(main.Conflicts, processors.Conflicts, tests.Conflicts),
			Missing:   concat(main.Missing, processors.Missing, tests.Missing),
		}
		toInstall = fromResolved(main.Packages)
		processorInstall = fromResolved(processors.Packages)
		testInstall = fromResolved(tests.Packages)
	}

	targetDir := cfg.ResolvePath(config.DefaultClassPath)
	total := len(toInstall) + len(processorInstall) + len(testInstall)

	result := Result{TargetDir: targetDir, FromLock: fromLock, LockStale: lockStale}
	progressed := 0
	// fetchOne runs concurrently (see materialize); the progress counter and the
	// OnProgress callback (a non-reentrant CLI progress bar) are the only shared
	// state, so serialize them.
	var progressMu sync.Mutex
	fetchOne := func(pkg pending, dir string) outcome {
		id := string(pkg.coordinates.String())
		bytes, cached, found := artifactFrom(srcs, pkg.source, pkg.coordinates)
		progressMu.Lock()
		progressed++
		if opts.OnProgress != nil {
			opts.OnProgress(progressed, total, pkg.coordinates.String())
		}
		progressMu.Unlock()
		if !found {
			return outcome{noArtifact: id}
		}
		digest := lockfile.Sha256Of(bytes)
		if pkg.hasSha && pkg.sha256 != digest {
			// A locked install must produce the locked bytes: do not write, and
			// evict the bad copy from the store (store-first, so it would
			// otherwise re-fail every install).
			if stored, ok := StorePathFor(pkg.coordinates); ok {
				_ = os.Remove(stored)
			}
			return outcome{integrity: id}
		}
		file := filepath.Join(dir, string(pkg.coordinates.ArtifactID)+"-"+string(pkg.coordinates.Version)+".jar")
		if err := os.WriteFile(file, bytes, 0o644); err != nil {
			return outcome{noArtifact: id}
		}
		locked := lockfile.NewLockedPackage(pkg.coordinates, pkg.source, digest, pkg.licenses)
		o := outcome{locked: &locked, installed: file}
		if cached {
			o.fromStore = id
		}
		return o
	}

	materialize := func(set []pending, dir string) ([]outcome, error) {
		if len(set) == 0 {
			return nil, nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		// Each package is independent, so the set is fetched with bounded
		// concurrency; results are written by index to keep input order
		// (the lock and result lists stay deterministic). fetchOne never errors.
		outcomes := make([]outcome, len(set))
		var g errgroup.Group
		g.SetLimit(downloadConcurrency)
		for i, pkg := range set {
			g.Go(func() error {
				outcomes[i] = fetchOne(pkg, dir)
				return nil
			})
		}
		_ = g.Wait()
		return outcomes, nil
	}

	mainOut, err := materialize(toInstall, targetDir)
	if err != nil {
		return Result{}, err
	}
	procOut, err := materialize(processorInstall, cfg.ResolvePath(config.DefaultProcessorPath))
	if err != nil {
		return Result{}, err
	}
	testOut, err := materialize(testInstall, cfg.ResolvePath(config.DefaultTestClassPath))
	if err != nil {
		return Result{}, err
	}

	mainLocked := result.assemble(mainOut, &result.InstalledByCategory.Compile)
	procLocked := result.assemble(procOut, &result.InstalledByCategory.Processor)
	testLocked := result.assemble(testOut, &result.InstalledByCategory.Test)

	// The lock pins what was VERIFIABLY materialized: written only when the
	// whole set arrived and resolution was complete.
	if !fromLock && cfg.FromFile && len(resolution.Missing) == 0 && len(result.NoArtifact) == 0 {
		newLock := &lockfile.Lockfile{
			Version:           2,
			Roots:             cfg.Dependencies,
			Packages:          mainLocked,
			ProcessorPackages: procLocked,
			TestPackages:      testLocked,
		}
		if err := lockfile.Write(cfg, newLock); err != nil {
			return Result{}, err
		}
	}
	result.Resolution = resolution
	return result, nil
}

// assemble collects outcomes into the result lists and returns the locked set
// for one group, appending its installed paths to groupInstalled.
func (r *Result) assemble(outcomes []outcome, groupInstalled *[]string) []lockfile.LockedPackage {
	var locked []lockfile.LockedPackage
	for _, o := range outcomes {
		switch {
		case o.noArtifact != "":
			r.NoArtifact = append(r.NoArtifact, o.noArtifact)
		case o.integrity != "":
			r.IntegrityFailures = append(r.IntegrityFailures, o.integrity)
		}
		if o.fromStore != "" {
			r.FromStore = append(r.FromStore, o.fromStore)
		}
		if o.installed != "" {
			r.Installed = append(r.Installed, o.installed)
			*groupInstalled = append(*groupInstalled, o.installed)
		}
		if o.locked != nil {
			locked = append(locked, *o.locked)
		}
	}
	return locked
}

// artifactFrom returns a package's jar bytes: the store first (no network),
// then the sources (the one that resolved it first). cached reports a store hit.
func artifactFrom(srcs []packages.PackageSource, preferred string, c packages.Coordinates) (bytes []byte, cached bool, found bool) {
	storePath, storeOK := StorePathFor(c)
	if storeOK {
		if data, err := os.ReadFile(storePath); err == nil {
			return data, true, true
		}
	}
	ordered := orderPreferred(srcs, preferred)
	for _, source := range ordered {
		data, err := source.GetArtifact(c)
		if err == nil && data != nil {
			if storeOK {
				if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err == nil {
					_ = os.WriteFile(storePath, data, 0o644) // a read-only store never fails the install
				}
			}
			return data, false, true
		}
	}
	return nil, false, false
}

func orderPreferred(srcs []packages.PackageSource, preferred string) []packages.PackageSource {
	ordered := make([]packages.PackageSource, 0, len(srcs))
	for _, s := range srcs {
		if s.Name() == preferred {
			ordered = append(ordered, s)
		}
	}
	for _, s := range srcs {
		if s.Name() != preferred {
			ordered = append(ordered, s)
		}
	}
	return ordered
}

func resolveIfAny(roots []packages.Coordinates, srcs []packages.PackageSource, onResolve func(packages.Coordinates)) (packages.Resolution, error) {
	if len(roots) == 0 {
		return packages.Resolution{}, nil
	}
	return packages.ResolveTransitive(roots, srcs, onResolve)
}

func fromResolved(pkgs []packages.ResolvedPackage) []pending {
	out := make([]pending, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, pending{coordinates: p.Coordinates, source: p.Source, licenses: p.Metadata.Licenses})
	}
	return out
}

func fromLocked(pkgs []lockfile.LockedPackage) []pending {
	out := make([]pending, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, pending{coordinates: p.Coords(), source: p.Source, sha256: p.Sha256, hasSha: true, licenses: p.Licenses})
	}
	return out
}

func concat[T any](slices ...[]T) []T {
	var out []T
	for _, s := range slices {
		out = append(out, s...)
	}
	return out
}
