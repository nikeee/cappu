package install

import (
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

func TestPickAddVersionNewestCompatible(t *testing.T) {
	cfg := project(t, `{}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.x:y:1.0": meta("org.x:y:1.0"),
			"org.x:y:2.0": meta("org.x:y:2.0"),
		},
		versions: map[string][]string{"org.x:y": {"1.0", "2.0"}},
	}
	picked, ok, err := PickAddVersion(cfg, "org.x:y", "", []packages.PackageSource{src})
	if err != nil || !ok {
		t.Fatalf("PickAddVersion ok=%v err=%v", ok, err)
	}
	if picked.Version != "2.0" || !picked.Compatible {
		t.Errorf("picked = %+v, want {2.0 true}", picked)
	}
}

func TestPickAddVersionRespectsSpec(t *testing.T) {
	cfg := project(t, `{}`)
	src := &fakeSource{
		name:     "test",
		meta:     map[packages.CoordinateString]packages.PackageMetadata{"org.x:y:1.5": meta("org.x:y:1.5")},
		versions: map[string][]string{"org.x:y": {"1.5", "2.0"}},
	}
	picked, ok, err := PickAddVersion(cfg, "org.x:y", "1", []packages.PackageSource{src})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if picked.Version != "1.5" {
		t.Errorf("picked = %+v, want version 1.5 (spec 1)", picked)
	}
}

func TestPickAddVersionNoMatch(t *testing.T) {
	cfg := project(t, `{}`)
	src := &fakeSource{name: "test", versions: map[string][]string{}}
	if _, ok, err := PickAddVersion(cfg, "org.x:y", "", []packages.PackageSource{src}); ok || err != nil {
		t.Errorf("expected no match (ok=false), got ok=%v err=%v", ok, err)
	}
}

func TestPlanUpdatesBumpsWithinMajor(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1.0"}}}`)
	src := &fakeSource{
		name: "test",
		meta: map[packages.CoordinateString]packages.PackageMetadata{
			"org.a:a:1.1": meta("org.a:a:1.1"),
		},
		versions: map[string][]string{"org.a:a": {"1.0", "1.1", "2.0"}},
	}
	bumps, err := PlanUpdates(cfg, []packages.PackageSource{src})
	if err != nil {
		t.Fatal(err)
	}
	// 1.0 -> 1.1 (stable, same major); 2.0 is a major bump and is skipped.
	want := []DependencyBump{{Configuration: "implementation", Key: "org.a:a", From: "1.0", To: "1.1"}}
	if len(bumps) != 1 || bumps[0] != want[0] {
		t.Errorf("bumps = %+v, want %+v", bumps, want)
	}
}

func TestPlanUpdatesSkipsPrerelease(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1.0"}}}`)
	src := &fakeSource{
		name:     "test",
		meta:     map[packages.CoordinateString]packages.PackageMetadata{"org.a:a:1.1": meta("org.a:a:1.1")},
		versions: map[string][]string{"org.a:a": {"1.0", "1.1", "1.2-SNAPSHOT"}},
	}
	bumps, err := PlanUpdates(cfg, []packages.PackageSource{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(bumps) != 1 || bumps[0].To != "1.1" {
		t.Errorf("bumps = %+v, want a single 1.0->1.1 (prerelease skipped)", bumps)
	}
}

func TestPlanUpdatesNoneWhenCurrent(t *testing.T) {
	cfg := project(t, `{"dependencies":{"implementation":{"org.a:a":"1.1"}}}`)
	src := &fakeSource{
		name:     "test",
		versions: map[string][]string{"org.a:a": {"1.0", "1.1"}},
	}
	bumps, err := PlanUpdates(cfg, []packages.PackageSource{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(bumps) != 0 {
		t.Errorf("expected no bumps at newest, got %+v", bumps)
	}
}
