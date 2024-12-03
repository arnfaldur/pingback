package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/prometheus-community/pro-bing"
)

func main() {
	// Parse command-line arguments
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

// Model represents the state of the application
type model struct {
	address  string
	interval time.Duration
	pinger   *probing.Pinger
	err      error

	latencyData []float64

	streamLength int
	// Streams for different timescales
	stream1 []float64         // Raw latency data
	stream2 [][]float64       // Aggregated data (32 points)
	stream3 [][]float64       // Aggregated data (1024 points)
	glyphs1 []string          // Glyphs for stream1
	glyphs2 []string          // Glyphs for stream2
	glyphs3 []string          // Glyphs for stream3
	minLat  float64           // Minimum latency observed
	maxLat  float64           // Maximum latency observed
}

func initialModel(address string, interval time.Duration) model {
	return model{
		address:  address,
		interval: interval,
		minLat:   math.MaxFloat64,
		maxLat:   0,
	}
}

// Init initializes the model
func (m model) Init() tea.Cmd {
	return m.pingCmd()
}

// pingCmd pings the target address and returns a latencyMsg
func (m model) pingCmd() tea.Cmd {
	return func() tea.Msg {
		pinger, err := probing.NewPinger(m.address)
		if err != nil {
			return errMsg{err}
		}
		pinger.Count = 1
		pinger.Timeout = m.interval
		// pinger.SetPrivileged(true)
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

// Messages used in the update function
type (
	latencyMsg struct {
		latency float64
	}
	errMsg struct {
		err error
	}
)

// Update handles incoming messages and updates the model state
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
	}

	return m, nil
}

// processLatency updates streams and glyphs based on the new latency data
func (m *model) processLatency(latency float64) {
	// Update min and max latency
	if !math.IsNaN(latency) {
		if latency < m.minLat {
			m.minLat = latency
		}
		if latency > m.maxLat {
			m.maxLat = latency
		}
	}

	// Update latency data
	m.latencyData = append(m.latencyData, latency)

	// Update stream1
	m.stream1 = append(m.stream1, latency)
	m.glyphs1 = append(m.glyphs1, m.latencyToGlyph(latency))
	if len(m.glyphs1) > m.streamLength {
		m.glyphs1 = m.glyphs1[1:]
	}

	// Update stream2
	if len(m.stream1)%32 == 0 {
		agg := m.stream1[len(m.stream1)-32:]
		m.stream2 = append(m.stream2, agg)
		m.glyphs2 = append(m.glyphs2, m.aggregateToGlyph(agg))
		if len(m.glyphs2) > m.streamLength {
			m.glyphs2 = m.glyphs2[1:]
		}
	}

	// Update stream3
	if len(m.stream1)%1024 == 0 {
		agg := m.stream1[len(m.stream1)-1024:]
		m.stream3 = append(m.stream3, agg)
		m.glyphs3 = append(m.glyphs3, m.aggregateToGlyph(agg))
		if len(m.glyphs3) > m.streamLength {
			m.glyphs3 = m.glyphs3[1:]
		}
	}
}

// View renders the UI
func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	header := fmt.Sprintf("Pinging %s every %v ms\n", m.address, m.interval.Milliseconds())
	gradient := m.renderGradient()

	streams := lipgloss.JoinVertical(lipgloss.Left,
		"Stream 1 (Raw Data):",
		m.renderGlyphs(m.glyphs1),
		"Stream 2 (Aggregated 32):",
		m.renderGlyphs(m.glyphs2),
		"Stream 3 (Aggregated 1024):",
		m.renderGlyphs(m.glyphs3),
	)

	return lipgloss.JoinVertical(lipgloss.Left, header, streams, "Latency Gradient:", gradient)
}

// Helper functions

// latencyToGlyph converts latency to a colored glyph
func (m model) latencyToGlyph(latency float64) string {
	var glyph string
	if math.IsNaN(latency) {
		glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Render("x")
	} else {
		color := m.latencyToColor(latency)
		glyph = lipgloss.NewStyle().Foreground(color).Render("█")
	}
	return glyph
}

// aggregateToGlyph creates glyphs for aggregated data (mean, min, max)
func (m model) aggregateToGlyph(data []float64) string {
	min, max, sum, count := math.MaxFloat64, 0.0, 0.0, 0.0
	for _, v := range data {
		if !math.IsNaN(v) {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			sum += v
			count++
		}
	}
	mean := sum / count

	minGlyph := m.latencyToGlyph(min)
	meanGlyph := m.latencyToGlyph(mean)
	maxGlyph := m.latencyToGlyph(max)

	return lipgloss.JoinHorizontal(lipgloss.Top, minGlyph, meanGlyph, maxGlyph)
}

// latencyToColor maps latency to a color gradient using logarithmic scaling
func (m model) latencyToColor(latency float64) lipgloss.Color {
	// Handle initial cases
	if m.minLat == m.maxLat {
		return lipgloss.Color("#00FF00") // Green
	}

	// Logarithmic scaling
	minLog := math.Log10(m.minLat + 1)
	maxLog := math.Log10(m.maxLat + 1)
	latLog := math.Log10(latency + 1)
	ratio := (latLog - minLog) / (maxLog - minLog)

	// Clamp ratio between 0 and 1
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}

	// Interpolate color from green to red
	r := int(255 * ratio)
	g := int(255 * (1 - ratio))
	return lipgloss.Color(fmt.Sprintf("#%02X%02X00", r, g))
}

// renderGlyphs joins glyphs into a string
func (m model) renderGlyphs(glyphs []string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, glyphs...)
}

// renderGradient displays the latency gradient scale
func (m model) renderGradient() string {
	gradient := ""
	steps := 10
	for i := 0; i <= steps; i++ {
		ratio := float64(i) / float64(steps)
		latency := math.Pow(10, ratio*(math.Log10(m.maxLat+1)-math.Log10(m.minLat+1))+math.Log10(m.minLat+1)) - 1
		color := m.latencyToColor(latency)
		label := fmt.Sprintf("%.2f ms", latency)
		gradient += lipgloss.NewStyle().Foreground(color).Render("█") + " " + label + "\n"
	}
	return gradient
}
