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
	stream1      []float64
	minLat       float64
	maxLat       float64
}

func initialModel(address string, interval time.Duration) model {
	return model{
		address:      address,
		interval:     interval,
		minLat:       math.MaxFloat64,
		maxLat:       0,
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
		if latency < m.minLat {
			m.minLat = latency
		}
		if latency > m.maxLat {
			m.maxLat = latency
		}
	}

	m.latencyData = append(m.latencyData, latency)
	if len(m.latencyData) > m.streamLength {
		m.latencyData = m.latencyData[1:]
	}

	m.stream1 = append(m.stream1, latency)
	if len(m.stream1) > m.streamLength {
		m.stream1 = m.stream1[1:]
	}
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	header := fmt.Sprintf("Pinging %s every %v ms\n",
		m.address, m.interval.Milliseconds())
	gradient := m.renderGradient()

	stream1 := m.renderStream(m.stream1)
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
	length := len(m.stream1) / size
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
		if end > len(m.stream1) {
			end = len(m.stream1)
		}
		agg := m.stream1[start:end]
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

func (m model) latencyToColor(latency float64) lipgloss.Color {
	if m.minLat == m.maxLat {
		return lipgloss.Color("#00FF00")
	}

	minLog := math.Log10(m.minLat + 1)
	maxLog := math.Log10(m.maxLat + 1)
	latLog := math.Log10(latency + 1)
	ratio := (latLog - minLog) / (maxLog - minLog)
	ratio = math.Max(0, math.Min(1, ratio))

	var r, g, b int
	switch {
	case ratio < 0.25:
		t := ratio / 0.25
		r = int(0 * (1 - t))
		g = int(0 * (1 - t))
		b = int(255 * (1 - t) + 0*t)
	case ratio < 0.5:
		t := (ratio - 0.25) / 0.25
		r = int(0 * t)
		g = int(255 * t)
		b = int(255 * (1 - t))
	case ratio < 0.75:
		t := (ratio - 0.5) / 0.25
		r = int(255 * t)
		g = 255
		b = 0
	default:
		t := (ratio - 0.75) / 0.25
		r = 255
		g = int(255 * (1 - t))
		b = 0
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, b))
}

func (m model) renderGradient() string {
	gradient := ""
	steps := 10
	for i := 0; i <= steps; i++ {
		ratio := float64(i) / float64(steps)
		latency := math.Pow(10, ratio*(math.Log10(m.maxLat+1)-
			math.Log10(m.minLat+1))+math.Log10(m.minLat+1)) - 1
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
