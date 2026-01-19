package nvidia

// GPUStats contains metrics collected from an NVIDIA GPU.
type GPUStats struct {
	Index            int     // GPU index (0, 1, 2, ...)
	Name             string  // GPU name (e.g., "NVIDIA GeForce RTX 3080")
	UUID             string  // GPU UUID
	UtilizationGPU   float64 // GPU utilization (%)
	UtilizationMem   float64 // Memory utilization (%)
	MemoryUsed       uint64  // Memory used (bytes)
	MemoryTotal      uint64  // Memory total (bytes)
	Temperature      float64 // Temperature (Celsius)
	PowerDraw        float64 // Power draw (Watts)
	PowerLimit       float64 // Power limit (Watts)
	FanSpeed         float64 // Fan speed (%)
	ClockGraphics    float64 // Graphics clock (MHz)
	ClockMemory      float64 // Memory clock (MHz)
	PerformanceState int     // Performance state (0-15, P0=max performance)
}
