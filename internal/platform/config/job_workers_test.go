package config

import "testing"

// TestJobWorkers covers the posture-aware worker-pool resolution: local compute
// keeps the small pool, offloaded STT+diar feeds the GPU batch optimum, and an
// explicit JobConcurrency always wins.
func TestJobWorkers(t *testing.T) {
	remote := ComputeRoute{Location: ComputeLocationRemote}
	local := ComputeRoute{Location: ComputeLocationLocal}

	cases := []struct {
		name string
		cfg  ComputeConfig
		want int
	}{
		{
			name: "all local → small pool",
			cfg:  ComputeConfig{Speech: local, Diarize: local, Embed: local},
			want: DefaultLocalJobWorkers,
		},
		{
			name: "offloaded stt+diar → GPU batch optimum",
			cfg:  ComputeConfig{Speech: remote, Diarize: remote, Embed: local},
			want: DefaultOffloadJobWorkers,
		},
		{
			name: "only speech remote → still local pool (diar drives the GPU)",
			cfg:  ComputeConfig{Speech: remote, Diarize: local},
			want: DefaultLocalJobWorkers,
		},
		{
			name: "explicit override wins over posture",
			cfg:  ComputeConfig{Speech: remote, Diarize: remote, JobConcurrency: 3},
			want: 3,
		},
		{
			name: "explicit override on local posture too",
			cfg:  ComputeConfig{Speech: local, Diarize: local, JobConcurrency: 16},
			want: 16,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.JobWorkers(); got != c.want {
				t.Errorf("JobWorkers() = %d; want %d", got, c.want)
			}
		})
	}
}
