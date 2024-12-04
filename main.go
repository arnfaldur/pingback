package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	probing "github.com/prometheus-community/pro-bing"
)

func main() {
	address := flag.String("address", "", "IP address or URL to ping")
	interval := flag.Int("interval", 1000, "Ping interval in milliseconds")
	flag.Parse()

	if *address == "" {
		fmt.Println("Usage: pingback -address=<IP_or_URL> [-interval=<milliseconds>]")
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(*address, time.Duration(*interval)*time.Millisecond))

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

type model struct {
	address      string
	interval     time.Duration
	err          error
	latencyData  []float64
	streamLength int
	minLatency   float64
	maxLatency   float64
}

func initialModel(address string, interval time.Duration) model {
	return model{
		address:  address,
		interval: interval,
		// minLatency:   math.MaxFloat64,
		// maxLatency:   0,
		minLatency:   1,
		maxLatency:   10000,
		streamLength: 80,
	}
}

func (m model) Init() tea.Cmd {
	return m.pingCmd()
}

func (m model) pingCmd() tea.Cmd {
	return func() tea.Msg {
		pinger, err := probing.NewPinger(m.address)
		if err != nil {
			return errMsg{err}
		}
		pinger.Count = 1
		pinger.Timeout = m.interval
		err = pinger.Run()
		if err != nil {
			return errMsg{err}
		}
		stats := pinger.Statistics()
		if len(stats.Rtts) > 0 {
			latency := stats.Rtts[0].Seconds() * 1000
			return latencyMsg{latency}
		}
		return latencyMsg{math.NaN()}
	}
}

type (
	latencyMsg struct{ latency float64 }
	errMsg     struct{ err error }
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case latencyMsg:
		m.processLatency(msg.latency)
		return m, tea.Tick(m.interval, func(t time.Time) tea.Msg {
			return m.pingCmd()()
		})
	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.streamLength = msg.Width
		if m.streamLength > 65536 {
			m.streamLength = 65536
		}
	}
	return m, nil
}

func (m *model) processLatency(latency float64) {
	if !math.IsNaN(latency) {
		if latency < m.minLatency {
			m.minLatency = latency
		}
		if latency > m.maxLatency {
			m.maxLatency = latency
		}
	}

	m.latencyData = append(m.latencyData, latency)
	if len(m.latencyData) > m.streamLength {
		m.latencyData = m.latencyData[1:]
	}
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	header := fmt.Sprintf("Pinging %s every %v ms\n",
		m.address, m.interval.Milliseconds())
	gradient := m.renderGradient()

	stream1 := m.renderStream(m.latencyData)
	stream2 := m.renderAggregateStream(32)
	stream3 := m.renderAggregateStream(1024)

	streams := lipgloss.JoinVertical(lipgloss.Left,
		"Stream 1 (Raw Data):", stream1,
		"Stream 2 (Aggregated 32):", stream2,
		"Stream 3 (Aggregated 1024):", stream3,
	)

	return lipgloss.JoinVertical(lipgloss.Left, header,
		streams, "Latency Gradient:", gradient)
}

func (m model) renderStream(data []float64) string {
	glyphs := make([]string, len(data))
	for i, lat := range data {
		glyphs[i] = m.latencyToGlyph(lat)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, glyphs...)
}

func (m model) renderAggregateStream(size int) string {
	length := len(m.latencyData) / size
	if length == 0 {
		return ""
	}
	glyphLines := make([][]string, 3)
	for i := 0; i < 3; i++ {
		glyphLines[i] = make([]string, length)
	}
	for i := 0; i < length; i++ {
		start := i * size
		end := start + size
		if end > len(m.latencyData) {
			end = len(m.latencyData)
		}
		agg := m.latencyData[start:end]
		minLat, meanLat, maxLat := minMeanMax(agg)
		glyphLines[0][i] = m.latencyToGlyph(maxLat)
		glyphLines[1][i] = m.latencyToGlyph(meanLat)
		glyphLines[2][i] = m.latencyToGlyph(minLat)
	}
	lines := make([]string, 3)
	for i := 0; i < 3; i++ {
		lines[i] = lipgloss.JoinHorizontal(lipgloss.Top,
			glyphLines[i]...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m model) latencyToGlyph(latency float64) string {
	if math.IsNaN(latency) {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#800080")).Render("X")
	}
	color := m.latencyToColor(latency)
	return lipgloss.NewStyle().Foreground(color).Render("█")
}

// Linear interpolation between two float64 values
func lerp(a, b, t float64) float64 {
	return a + t*(b-a)
}

// Linear interpolation between two lipgloss.Color values
func lerpColor(colorA, colorB lipgloss.Color, t float64) lipgloss.Color {
	// doesn't work for some reason:
	// r1, g1, b1, _ := colorA.RGBA();
	// r2, g2, b2, _ := colorB.RGBA();

	var r1, g1, b1 int
	var r2, g2, b2 int

	fmt.Sscanf(string(colorA), "#%02x%02x%02x", &r1, &g1, &b1)
	fmt.Sscanf(string(colorB), "#%02x%02x%02x", &r2, &g2, &b2)

	r := int(lerp(float64(r1), float64(r2), t))
	g := int(lerp(float64(g1), float64(g2), t))
	b := int(lerp(float64(b1), float64(b2), t))

	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, b))
}

// Get gradient color based on ratio
func getGradientColor(colors []lipgloss.Color, ratio float64) lipgloss.Color {
	if ratio <= 0 {
		return colors[0]
	}
	if ratio >= 1 {
		return colors[len(colors)-1]
	}
	scaledRatio := ratio * float64(len(colors)-1)
	index := int(scaledRatio)
	t := scaledRatio - float64(index)
	return lerpColor(colors[index], colors[index+1], t)
}

// Updated latencyToColor function
func (m model) latencyToColor(latency float64) lipgloss.Color {
	if m.minLatency == m.maxLatency {
		return lipgloss.Color("#00FF00") // Default to green
	}

	minLog := math.Log10(m.minLatency + 1)
	maxLog := math.Log10(m.maxLatency + 1)
	latLog := math.Log10(latency + 1)
	ratio := (latLog - minLog) / (maxLog - minLog)
	ratio = math.Max(0, math.Min(1, ratio))

	// Define your gradient colors
	// gradientColors := []lipgloss.Color{
	// 	lipgloss.Color("#42A5F5"), // Soft blue
	// 	lipgloss.Color("#4CAF50"), // Muted green
	// 	lipgloss.Color("#A2D94A"), // Lime green
	// 	lipgloss.Color("#FFD700"), // Golden yellow
	// 	lipgloss.Color("#FFA500"), // Orange
	// 	lipgloss.Color("#FF4500"), // Orange-red
	// 	lipgloss.Color("#C42020"), // Deep red
	// }

	gradientColors := []lipgloss.Color{
		lipgloss.Color("#74D7FF"), // Bright cyan-blue
		lipgloss.Color("#80FF80"), // Bright green
		// lipgloss.Color("#E8FF80"), // Bright lime-green
		lipgloss.Color("#FFFF80"), // Bright yellow
		// lipgloss.Color("#FFCC80"), // Bright orange
		lipgloss.Color("#FF8080"), // Bright red

		lipgloss.Color("#1C2E50"), // Dark cyan-blue
		lipgloss.Color("#205020"), // Dark green
		// lipgloss.Color("#4A5C20"), // Dark lime-green
		lipgloss.Color("#665000"), // Dark yellow
		lipgloss.Color("#663C20"), // Dark orange
		lipgloss.Color("#500000"), // Dark red
	}

	// gradientColors := []lipgloss.Color{
	//     // lipgloss.Color("#00FFFF"), // Blue
	//     lipgloss.Color("#00FF00"), // Green
	//     lipgloss.Color("#FFFF00"), // Yellow
	//     lipgloss.Color("#FF0000"), // Red
	// }

	return getGradientColor(gradientColors, ratio)
}

func (m model) renderGradient() string {
	gradient := ""
	steps := 9 - 1
	for i := 0; i <= steps; i++ {
		ratio := float64(i) / float64(steps)
		latency := math.Pow(10, ratio*(math.Log10(m.maxLatency)-
			math.Log10(m.minLatency))+
			math.Log10(m.minLatency))
		color := m.latencyToColor(latency)
		label := fmt.Sprintf("%.2f ms", latency)
		gradient += lipgloss.NewStyle().
			Foreground(color).Render("█") + " " + label + "\n"
	}
	return gradient
}

func minMeanMax(data []float64) (float64, float64, float64) {
	min := math.MaxFloat64
	max := 0.0
	sum := 0.0
	count := 0.0
	for _, v := range data {
		if !math.IsNaN(v) {
			min = math.Min(min, v)
			max = math.Max(max, v)
			sum += v
			count++
		}
	}
	if count == 0 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	return min, sum / count, max
}
