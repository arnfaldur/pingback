package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	probing "github.com/prometheus-community/pro-bing"
)

const aggregateCount1 = 4
const aggregateCount2 = aggregateCount1 * aggregateCount1

func main() {
	address := flag.String("address", "", "IP address or URL to ping")
	interval := flag.Int("delay", 1000, "Delay between pings in milliseconds")
	flag.Parse()

	if *address == "" {
		fmt.Println("Usage: pingback -address=<IP_or_URL> [-delay=<milliseconds>]")
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(*address, time.Duration(*interval)*time.Millisecond))

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

type model struct {
	address       string
	interval      time.Duration
	err           error
	counter       int64
	latencyData   []float64
	aggregateData [][][]float64
	windowWidth   int
	minLatency    float64
	maxLatency    float64
}

func initialModel(address string, interval time.Duration) model {
	aggregateData := make([][][]float64, 2)
	for i := range aggregateData {
		aggregateData[i] = make([][]float64, 3)
	}
	return model{
		aggregateData: aggregateData,
		address:       address,
		interval:      interval,
		minLatency:    math.MaxFloat64,
		maxLatency:    0.001,
		// minLatency:  1,
		// maxLatency:  10000,
		windowWidth: 80,
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
		m.windowWidth = msg.Width
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

	// m.latencyData = m.latencyData[1:]
	if len(m.latencyData) > m.windowWidth*65536 {
		m.latencyData = m.latencyData[1:]
	}
	m.counter += 1
	if m.counter%aggregateCount1 == 0 && len(m.latencyData) > 0 {
		// minimum, average, maximum := minAvgMax(m.latencyData[len(m.latencyData)-aggregateCount1:])
		aggregate := minAvgMax(m.latencyData[len(m.latencyData)-aggregateCount1:])
		for i := range m.aggregateData[0] {
			m.aggregateData[0][i] = append(m.aggregateData[0][i], aggregate[i])
		}
		// m.aggregateData[0].maximum = append(m.aggregateData[0].maximum, maximum)
		// m.aggregateData[0].average = append(m.aggregateData[0].average, average)
		// m.aggregateData[0].minimum = append(m.aggregateData[0].minimum, minimum)
		// l := m.latencyData[len(m.latencyData)-1]
		// m.aggregateData[0] = append(m.aggregateData[0], aggregate{l, l, l})
	}

	if m.counter%aggregateCount2 == 0 && len(m.latencyData) > 0 {
		aggregate := minAvgMax(m.latencyData[len(m.latencyData)-aggregateCount2:])
		for i := range m.aggregateData[1] {
			m.aggregateData[1][i] = append(m.aggregateData[1][i], aggregate[i])
		}
		// minimum, average, maximum := minAvgMax(m.latencyData[len(m.latencyData)-aggregateCount2:])
		// m.aggregateData[1].maximum = append(m.aggregateData[1].maximum, maximum)
		// m.aggregateData[1].average = append(m.aggregateData[1].average, average)
		// m.aggregateData[1].minimum = append(m.aggregateData[1].minimum, minimum)
		// l := m.latencyData[len(m.latencyData)-1]
		// m.aggregateData[1] = append(m.aggregateData[1], aggregate{l, l, l})
		m.counter -= aggregateCount2
	}
}

func (m model) getDisplayableStreamEnd(stream []float64) []float64 {
	return stream[max(0, len(stream)-m.windowWidth):]
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	header := fmt.Sprintf("Pinging %s every %v ms\n",
		m.address, m.interval.Milliseconds())
	gradient := m.renderLegend()

	stream1 := m.renderStream(m.getDisplayableStreamEnd(m.latencyData))
	aggregateDisplayable1 := make([]string, len(m.aggregateData[0]))
	for i, data := range m.aggregateData[0] {
		aggregateDisplayable1[i] = m.renderStream(m.getDisplayableStreamEnd(data))
	}
	stream2 := lipgloss.JoinVertical(lipgloss.Top,
		aggregateDisplayable1...,
	)
	// stream2 := lipgloss.JoinVertical(lipgloss.Top,
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[0].maximum)),
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[0].average)),
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[0].minimum)))

	aggregateDisplayable2 := make([]string, len(m.aggregateData[1]))
	for i, data := range m.aggregateData[1] {
		aggregateDisplayable2[i] = m.renderStream(m.getDisplayableStreamEnd(data))
	}
	stream3 := lipgloss.JoinVertical(lipgloss.Top,
		aggregateDisplayable2...,
	)
	// stream3 := lipgloss.JoinVertical(lipgloss.Top,
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[1].maximum)),
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[1].average)),
	// 	m.renderStream(m.getDisplayableStreamEnd(m.aggregateData[1].minimum)))

	streams := lipgloss.JoinVertical(lipgloss.Left,
		"Stream 1 (Raw Data):", stream1,
		"Stream 2 (Aggregated "+fmt.Sprint(aggregateCount1)+"):", stream2,
		"Stream 3 (Aggregated "+fmt.Sprint(aggregateCount2)+"):", stream3,
	)

	return lipgloss.JoinVertical(lipgloss.Left, header,
		streams, "Latency Gradient (ms):", gradient)
}

func (m model) renderStream(data []float64) string {
	glyphs := make([]string, len(data))
	for i, lat := range data {
		glyphs[i] = m.latencyToGlyph(lat)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, glyphs...)
}

func (m model) latencyToGlyph(latency float64) string {
	if math.IsNaN(latency) {
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#600060")).Render("X")
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
	// doesn't work as expected for some reason:
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

	// minLog := math.Log10(m.minLatency)
	// maxLog := math.Log10(m.maxLatency)
	// latLog := math.Log10(latency)
	// ratio := (latLog - minLog) / (maxLog - minLog)
	// ratio = math.Max(0, math.Min(1, ratio))
	ratio := math.Log(latency/m.minLatency) / math.Log(m.maxLatency/m.minLatency)

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

		// lipgloss.Color("#1C2E50"), // Dark cyan-blue
		// lipgloss.Color("#205020"), // Dark green
		// // lipgloss.Color("#4A5C20"), // Dark lime-green
		// lipgloss.Color("#665000"), // Dark yellow
		// lipgloss.Color("#663C20"), // Dark orange
		// lipgloss.Color("#500000"), // Dark red
	}

	return getGradientColor(gradientColors, ratio)
}

func (m model) renderLegend() string {
	// Number of gradient steps
	steps := 90 - 1
	// Collect legend entries
	entries := make([]string, steps+1)
	lengths := make([]int, steps+1)
	for i := 0; i <= steps; i++ {
		ratio := float64(i) / float64(steps)
		latency := m.minLatency * math.Exp(ratio*math.Log(m.maxLatency/m.minLatency))
		label := fmt.Sprintf("%.1f", latency)
		if latency >= 100 {
			label = fmt.Sprintf("%.0f", latency)
		}
		entries[i] = m.latencyToGlyph(latency) + " " + label + " "
		lengths[i] = 3 + len(label)
	}

	widestEntry := 0
	for _, length := range lengths {
		if length > widestEntry {
			widestEntry = length
		}
	}

	for i := range entries {
		entries[i] += strings.Repeat(" ", widestEntry-lengths[i])
	}

	// Get terminal width
	cols := m.windowWidth / widestEntry // Approximate column width
	if cols < 1 {
		cols = 1
	}

	// Generate column-major grid
	rows := (len(entries) + cols - 1) / cols
	grid := make([][]string, cols)
	for i := range grid {
		grid[i] = make([]string, rows)
	}

	for i, entry := range entries {
		col := i / rows
		row := i % rows
		grid[col][row] = entry
	}

	// Join rows into a table
	rowsJoined := make([]string, len(grid))
	for i, row := range grid {
		rowsJoined[i] = lipgloss.JoinVertical(lipgloss.Left, row...)
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, rowsJoined...)
}

func minAvgMax(data []float64) [3]float64 {
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
		return [...]float64{math.NaN(), math.NaN(), math.NaN()}
	}
	return [...]float64{min, sum / count, max}
}
