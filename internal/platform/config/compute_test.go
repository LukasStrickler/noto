package config

import "testing"

func TestComputeRouteDefaultsLocal(t *testing.T) {
	c := DefaultConfig().Compute
	for _, r := range []ComputeRoute{c.Speech, c.Diarize, c.Embed} {
		if _, remote := c.Resolve(r); remote {
			t.Errorf("default route should be local, got remote: %+v", r)
		}
	}
	if c.AnyRemote() {
		t.Error("default compute should report AnyRemote=false")
	}
}

func TestComputeRouteInheritsSharedEndpoint(t *testing.T) {
	c := ComputeConfig{
		Endpoint: ComputeEndpoint{URL: "https://gpu.box", TokenRef: "compute:gpu"},
		Speech:   ComputeRoute{Location: ComputeLocationRemote}, // no URL → inherits Endpoint
		Diarize:  ComputeRoute{Location: ComputeLocationLocal},
	}
	ep, remote := c.Resolve(c.Speech)
	if !remote {
		t.Fatal("speech should be remote")
	}
	if ep.URL != "https://gpu.box" || ep.TokenRef != "compute:gpu" {
		t.Errorf("speech should inherit shared endpoint, got %+v", ep)
	}
	if _, remote := c.Resolve(c.Diarize); remote {
		t.Error("diarize is local; should not be remote")
	}
	if !c.AnyRemote() {
		t.Error("AnyRemote should be true when speech is offloaded")
	}
}

func TestComputeRoutePerCapabilityURLWins(t *testing.T) {
	c := ComputeConfig{
		Endpoint: ComputeEndpoint{URL: "https://shared"},
		Speech:   ComputeRoute{Location: ComputeLocationRemote, URL: "modal://app/stt", TokenRef: "compute:modal"},
	}
	ep, remote := c.Resolve(c.Speech)
	if !remote || ep.URL != "modal://app/stt" || ep.TokenRef != "compute:modal" {
		t.Errorf("per-capability URL should win over shared endpoint, got remote=%v %+v", remote, ep)
	}
}

func TestComputeRemoteWithoutURLIsLocal(t *testing.T) {
	// location=remote but no URL anywhere → treated as local, never a broken
	// half-configured remote.
	c := ComputeConfig{Speech: ComputeRoute{Location: ComputeLocationRemote}}
	if _, remote := c.Resolve(c.Speech); remote {
		t.Error("remote location with no URL must resolve to local")
	}
}

func TestComputeConfigRoundTripsThroughSave(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Compute.Endpoint.URL = "https://gpu.box:8731"
	cfg.Compute.Speech.Location = ComputeLocationRemote
	cfg.Compute.Diarize.Location = ComputeLocationLocal
	cfg.Compute.Embed.Location = ComputeLocationLocal
	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Compute.Endpoint.URL != "https://gpu.box:8731" {
		t.Errorf("endpoint url = %q", loaded.Compute.Endpoint.URL)
	}
	if _, remote := loaded.Compute.Resolve(loaded.Compute.Speech); !remote {
		t.Error("speech route should round-trip as remote")
	}
	if _, remote := loaded.Compute.Resolve(loaded.Compute.Diarize); remote {
		t.Error("diarize route should round-trip as local")
	}
}
