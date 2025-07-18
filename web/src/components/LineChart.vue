<script setup lang="ts">
import { getDashboardChart, getGroupList } from "@/api/dashboard";
import type { ChartData } from "@/types/models";
import { getGroupDisplayName } from "@/utils/display";
import { NSelect, NSpin } from "naive-ui";
import { computed, onMounted, ref, watch } from "vue";

// Chart data
const chartData = ref<ChartData | null>(null);
const selectedGroup = ref<number | null>(null);
const loading = ref(true);
const animationProgress = ref(0);
const hoveredPoint = ref<{
  datasetIndex: number;
  pointIndex: number;
  x: number;
  y: number;
} | null>(null);
const tooltipData = ref<{
  time: string;
  datasets: Array<{
    label: string;
    value: number;
    color: string;
  }>;
} | null>(null);
const tooltipPosition = ref({ x: 0, y: 0 });
const chartSvg = ref<SVGElement>();

// Chart dimensions and margins
const chartWidth = 800;
const chartHeight = 260;
const padding = { top: 40, right: 40, bottom: 60, left: 80 };

// Format group options
const groupOptions = ref<Array<{ label: string; value: number | null }>>([]);

// Calculate the effective plotting area
const plotWidth = chartWidth - padding.left - padding.right;
const plotHeight = chartHeight - padding.top - padding.bottom;

// Calculate the maximum and minimum values of the data
const dataRange = computed(() => {
  if (!chartData.value) {
    return { min: 0, max: 100 };
  }

  const allValues = chartData.value.datasets.flatMap(d => d.data);
  const max = Math.max(...allValues, 0);
  const min = Math.min(...allValues, 0);

  // If all data is 0, set a reasonable range
  if (max === 0 && min === 0) {
    return { min: 0, max: 10 };
  }

  // Add some padding to make the chart look better
  const paddingValue = Math.max((max - min) * 0.1, 1);
  return {
    min: Math.max(0, min - paddingValue),
    max: max + paddingValue,
  };
});

// Generate Y-axis ticks
const yTicks = computed(() => {
  const { min, max } = dataRange.value;
  const range = max - min;
  const tickCount = 5;
  const step = range / (tickCount - 1);

  return Array.from({ length: tickCount }, (_, i) => min + i * step);
});

// Format time labels
const formatTimeLabel = (isoString: string) => {
  const date = new Date(isoString);
  return date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
};

// Generate visible X-axis labels (avoid overlapping)
const visibleLabels = computed(() => {
  if (!chartData.value) {
    return [];
  }

  const labels = chartData.value.labels;
  const maxLabels = 8; // Maximum 8 labels to display
  const step = Math.ceil(labels.length / maxLabels);

  return labels
    .map((label, index) => ({ text: formatTimeLabel(label), index }))
    .filter((_, i) => i % step === 0);
});

// Position calculation functions
const getXPosition = (index: number) => {
  if (!chartData.value) {
    return 0;
  }
  const totalPoints = chartData.value.labels.length;
  return padding.left + (index / (totalPoints - 1)) * plotWidth;
};

const getYPosition = (value: number) => {
  const { min, max } = dataRange.value;
  const ratio = (value - min) / (max - min);
  return padding.top + (1 - ratio) * plotHeight;
};

// Generate line path (handle zero value points)
const generateLinePath = (data: number[]) => {
  if (!data.length) {
    return "";
  }

  const points: string[] = [];
  let hasValidPath = false;

  data.forEach((value, index) => {
    const x = getXPosition(index);
    const y = getYPosition(value);

    if (value > 0) {
      if (!hasValidPath) {
        points.push(`M ${x},${y}`);
        hasValidPath = true;
      } else {
        points.push(`L ${x},${y}`);
      }
    } else if (hasValidPath && index < data.length - 1) {
      // If current value is zero but there's a valid path before, check if there are non-zero values ahead
      const nextNonZeroIndex = data.findIndex((v, i) => i > index && v > 0);
      if (nextNonZeroIndex !== -1) {
        // If there are non-zero values ahead, end the current path
        hasValidPath = false;
      }
    }
  });

  return points.join(" ");
};

// Generate fill area path (only fill areas with data)
const generateAreaPath = (data: number[]) => {
  if (!data.length) {
    return "";
  }

  const validPoints: Array<{ x: number; y: number; index: number }> = [];

  data.forEach((value, index) => {
    if (value > 0) {
      const x = getXPosition(index);
      const y = getYPosition(value);
      validPoints.push({ x, y, index });
    }
  });

  if (validPoints.length === 0) {
    return "";
  }

  const baseY = getYPosition(dataRange.value.min);
  const pathPoints = validPoints.map(p => `${p.x},${p.y}`);

  // Start from the bottom, draw to each point, then back to bottom
  const firstPoint = validPoints[0];
  const lastPoint = validPoints[validPoints.length - 1];

  return `M ${firstPoint.x},${baseY} L ${pathPoints.join(" L ")} L ${lastPoint.x},${baseY} Z`;
};

// Number formatting
const formatNumber = (value: number) => {
  // if (value >= 1000000) {
  //   return `${(value / 1000000).toFixed(1)}M`;
  // } else
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K`;
  }
  return Math.round(value).toString();
};

// Animation related
const animatedStroke = ref("0");
const animatedOffset = ref("0");

const startAnimation = () => {
  if (!chartData.value) {
    return;
  }

  // Calculate total path length (approximate)
  const totalLength = plotWidth + plotHeight;
  animatedStroke.value = `${totalLength}`;
  animatedOffset.value = `${totalLength}`;

  let start = 0;
  const animate = (timestamp: number) => {
    if (!start) {
      start = timestamp;
    }
    const progress = Math.min((timestamp - start) / 1500, 1);

    animatedOffset.value = `${totalLength * (1 - progress)}`;
    animationProgress.value = progress;

    if (progress < 1) {
      requestAnimationFrame(animate);
    }
  };
  requestAnimationFrame(animate);
};

// Mouse interaction
const handleMouseMove = (event: MouseEvent) => {
  if (!chartData.value || !chartSvg.value) {
    return;
  }

  const rect = chartSvg.value.getBoundingClientRect();
  // Consider SVG viewBox scaling
  const scaleX = 800 / rect.width;
  const scaleY = 260 / rect.height;

  const mouseX = (event.clientX - rect.left) * scaleX;
  const mouseY = (event.clientY - rect.top) * scaleY;

  // First find the closest X-axis position (time point)
  let closestXDistance = Infinity;
  let closestTimeIndex = -1;

  chartData.value.labels.forEach((_, pointIndex) => {
    const x = getXPosition(pointIndex);
    const xDistance = Math.abs(mouseX - x);

    if (xDistance < closestXDistance) {
      closestXDistance = xDistance;
      closestTimeIndex = pointIndex;
    }
  });

  // If the mouse is too far from the closest time point, don't show the tooltip
  if (closestXDistance > 50) {
    hoveredPoint.value = null;
    tooltipData.value = null;
    return;
  }

  // Collect data from all datasets at this time point
  const datasetsAtTime = chartData.value.datasets.map(dataset => ({
    label: dataset.label,
    value: dataset.data[closestTimeIndex],
    color: dataset.color,
  }));

  if (closestTimeIndex >= 0) {
    hoveredPoint.value = {
      datasetIndex: 0, // No longer need specific dataset index
      pointIndex: closestTimeIndex,
      x: mouseX,
      y: mouseY,
    };

    // Show tooltip
    const x = getXPosition(closestTimeIndex);
    const avgY =
      datasetsAtTime.reduce((sum, item) => sum + getYPosition(item.value), 0) /
      datasetsAtTime.length;

    tooltipPosition.value = {
      x,
      y: avgY - 20, // Display above average height
    };

    tooltipData.value = {
      time: formatTimeLabel(chartData.value.labels[closestTimeIndex]),
      datasets: datasetsAtTime,
    };
  } else {
    hoveredPoint.value = null;
    tooltipData.value = null;
  }
};

const hideTooltip = () => {
  hoveredPoint.value = null;
  tooltipData.value = null;
};

// Get group list
const fetchGroups = async () => {
  try {
    const response = await getGroupList();
    groupOptions.value = [
      { label: "All Groups", value: null },
      ...response.data.map(group => ({
        label: getGroupDisplayName(group),
        value: group.id || 0,
      })),
    ];
  } catch (error) {
    console.error("Failed to get group list:", error);
  }
};

// Get chart data
const fetchChartData = async () => {
  try {
    loading.value = true;
    const response = await getDashboardChart(selectedGroup.value || undefined);
    chartData.value = response.data;

    // Delay starting animation to ensure DOM updates are complete
    setTimeout(() => {
      startAnimation();
    }, 100);
  } catch (error) {
    console.error("Failed to get chart data:", error);
  } finally {
    loading.value = false;
  }
};

// Watch for group selection changes
watch(selectedGroup, () => {
  fetchChartData();
});

onMounted(() => {
  fetchGroups();
  fetchChartData();
});
</script>

<template>
  <div class="chart-container">
    <div class="chart-header">
      <div class="chart-title-section">
        <h3 class="chart-title">24h Request Trend</h3>
        <p class="chart-subtitle">Real-time monitoring of system request status</p>
      </div>
      <n-select
        v-model:value="selectedGroup"
        :options="groupOptions as any"
        placeholder="All Groups"
        size="small"
        style="width: 150px"
        clearable
      />
    </div>

    <div v-if="chartData" class="chart-content">
      <div class="chart-legend">
        <div v-for="dataset in chartData.datasets" :key="dataset.label" class="legend-item">
          <div class="legend-indicator" :style="{ backgroundColor: dataset.color }" />
          <span class="legend-label">{{ dataset.label }}</span>
        </div>
      </div>

      <div class="chart-wrapper">
        <svg
          ref="chartSvg"
          viewBox="0 0 800 260"
          class="chart-svg"
          @mousemove="handleMouseMove"
          @mouseleave="hideTooltip"
        >
          <!-- Background grid -->
          <defs>
            <pattern id="grid" width="40" height="30" patternUnits="userSpaceOnUse">
              <path
                d="M 40 0 L 0 0 0 30"
                fill="none"
                stroke="#f0f0f0"
                stroke-width="1"
                opacity="0.3"
              />
            </pattern>
          </defs>
          <rect width="100%" height="100%" fill="url(#grid)" />

          <!-- Y-axis ticks and labels -->
          <g class="y-axis">
            <line
              :x1="padding.left"
              :y1="padding.top"
              :x2="padding.left"
              :y2="chartHeight - padding.bottom"
              stroke="#e0e0e0"
              stroke-width="2"
            />
            <g v-for="(tick, index) in yTicks" :key="index">
              <line
                :x1="padding.left - 5"
                :y1="getYPosition(tick)"
                :x2="padding.left"
                :y2="getYPosition(tick)"
                stroke="#666"
                stroke-width="1"
              />
              <text
                :x="padding.left - 10"
                :y="getYPosition(tick) + 4"
                text-anchor="end"
                class="axis-label"
              >
                {{ formatNumber(tick) }}
              </text>
            </g>
          </g>

          <!-- X-axis ticks and labels -->
          <g class="x-axis">
            <line
              :x1="padding.left"
              :y1="chartHeight - padding.bottom"
              :x2="chartWidth - padding.right"
              :y2="chartHeight - padding.bottom"
              stroke="#e0e0e0"
              stroke-width="2"
            />
            <g v-for="(label, index) in visibleLabels" :key="index">
              <line
                :x1="getXPosition(label.index)"
                :y1="chartHeight - padding.bottom"
                :x2="getXPosition(label.index)"
                :y2="chartHeight - padding.bottom + 5"
                stroke="#666"
                stroke-width="1"
              />
              <text
                :x="getXPosition(label.index)"
                :y="chartHeight - padding.bottom + 18"
                text-anchor="middle"
                class="axis-label"
              >
                {{ label.text }}
              </text>
            </g>
          </g>

          <!-- Data lines -->
          <g v-for="(dataset, datasetIndex) in chartData.datasets" :key="dataset.label">
            <!-- Gradient definitions -->
            <defs>
              <linearGradient :id="`gradient-${datasetIndex}`" x1="0%" y1="0%" x2="0%" y2="100%">
                <stop offset="0%" :stop-color="dataset.color" stop-opacity="0.3" />
                <stop offset="100%" :stop-color="dataset.color" stop-opacity="0.05" />
              </linearGradient>
            </defs>

            <!-- Fill area -->
            <path
              :d="generateAreaPath(dataset.data)"
              :fill="`url(#gradient-${datasetIndex})`"
              class="area-path"
            />

            <!-- Main line -->
            <path
              :d="generateLinePath(dataset.data)"
              :stroke="dataset.color"
              stroke-width="2"
              fill="none"
              class="line-path"
              :style="{
                strokeDasharray: animatedStroke,
                strokeDashoffset: animatedOffset,
                filter: 'drop-shadow(0 1px 3px rgba(0,0,0,0.1))',
              }"
            />

            <!-- Data points -->
            <g v-for="(value, pointIndex) in dataset.data" :key="pointIndex">
              <circle
                v-if="value > 0"
                :cx="getXPosition(pointIndex)"
                :cy="getYPosition(value)"
                r="3"
                :fill="dataset.color"
                :stroke="dataset.color"
                stroke-width="1"
                class="data-point"
                :class="{
                  'point-hover': hoveredPoint?.pointIndex === pointIndex,
                }"
              />
              <!-- Zero value points represented by small gray dots -->
              <circle
                v-else
                :cx="getXPosition(pointIndex)"
                :cy="getYPosition(value)"
                r="1.5"
                fill="#d1d5db"
                stroke="#d1d5db"
                stroke-width="1"
                class="data-point-zero"
                opacity="0.6"
              />
            </g>
          </g>

          <!-- Hover indicator line -->
          <line
            v-if="hoveredPoint"
            :x1="getXPosition(hoveredPoint.pointIndex)"
            :y1="padding.top"
            :x2="getXPosition(hoveredPoint.pointIndex)"
            :y2="chartHeight - padding.bottom"
            stroke="#999"
            stroke-width="1"
            stroke-dasharray="5,5"
            opacity="0.7"
          />
        </svg>

        <!-- Tooltip -->
        <div
          v-if="tooltipData"
          class="chart-tooltip"
          :style="{
            left: tooltipPosition.x + 'px',
            top: tooltipPosition.y + 'px',
          }"
        >
          <div class="tooltip-time">{{ tooltipData.time }}</div>
          <div v-for="dataset in tooltipData.datasets" :key="dataset.label" class="tooltip-value">
            <span class="tooltip-color" :style="{ backgroundColor: dataset.color }" />
            {{ dataset.label }}: {{ formatNumber(dataset.value) }}
          </div>
        </div>
      </div>
    </div>

    <div v-else class="chart-loading">
      <n-spin size="large" />
      <p>Loading...</p>
    </div>
  </div>
</template>

<style scoped>
.chart-container {
  padding: 20px;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border-radius: 16px;
  backdrop-filter: blur(4px);
  border: 1px solid rgba(255, 255, 255, 0.18);
  color: white;
}

.chart-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 20px;
  gap: 16px;
}

.chart-title-section {
  flex: 1;
}

.chart-title {
  margin: 0 0 4px 0;
  font-size: 24px;
  font-weight: 600;
  background: linear-gradient(45deg, #fff, #f0f0f0);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.chart-subtitle {
  margin: 0;
  font-size: 14px;
  color: rgba(255, 255, 255, 0.8);
  font-weight: 400;
}

.chart-content {
  background: rgba(255, 255, 255, 0.95);
  border-radius: 12px;
  padding: 12px;
  color: #333;
}

.chart-legend {
  display: flex;
  justify-content: center;
  gap: 12px;
  margin-bottom: 12px;
}

.legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  font-size: 13px;
  color: #475569;
  padding: 8px 16px;
  background: rgba(255, 255, 255, 0.6);
  border-radius: 20px;
  border: 1px solid rgba(226, 232, 240, 0.6);
  transition: all 0.2s ease;
}

.legend-item:hover {
  background: rgba(255, 255, 255, 0.9);
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.legend-indicator {
  width: 12px;
  height: 12px;
  border-radius: 3px;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
  position: relative;
}

.legend-indicator::after {
  content: "";
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 6px;
  height: 6px;
  background: rgba(255, 255, 255, 0.3);
  border-radius: 50%;
}

.legend-label {
  font-size: 13px;
  color: #334155;
}

.chart-wrapper {
  position: relative;
  display: flex;
  justify-content: center;
}

.chart-svg {
  background: white;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
}

.axis-label {
  fill: #666;
  font-size: 12px;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}

.line-path {
  transition: all 0.3s ease;
}

.area-path {
  opacity: 0.6;
  transition: opacity 0.3s ease;
}

.data-point {
  cursor: pointer;
  transition: all 0.2s ease;
}

.data-point:hover,
.point-hover {
  r: 5;
  filter: drop-shadow(0 0 6px rgba(0, 0, 0, 0.3));
}

.data-point-zero {
  cursor: default;
  transition: opacity 0.2s ease;
}

.data-point-zero:hover {
  opacity: 0.8;
}

.chart-tooltip {
  position: absolute;
  background: rgba(0, 0, 0, 0.9);
  color: white;
  padding: 12px 16px;
  border-radius: 8px;
  font-size: 13px;
  pointer-events: none;
  transform: translateX(-50%) translateY(-100%);
  z-index: 1000;
  backdrop-filter: blur(8px);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.1);
  min-width: 140px;
  max-width: 220px;
}

.tooltip-time {
  font-weight: 700;
  margin-bottom: 8px;
  text-align: center;
  color: #e2e8f0;
  font-size: 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.2);
  padding-bottom: 6px;
}

.tooltip-value {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  margin-bottom: 4px;
  font-size: 12px;
}

.tooltip-value:last-child {
  margin-bottom: 0;
}

.tooltip-color {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.chart-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 260px;
  color: white;
}

.chart-loading p {
  margin-top: 16px;
  font-size: 16px;
  opacity: 0.8;
}

/* Responsive design */
@media (max-width: 768px) {
  .chart-container {
    padding: 16px;
  }

  .chart-title {
    font-size: 20px;
  }

  .chart-header {
    flex-direction: column;
    gap: 12px;
    align-items: flex-start;
  }

  .chart-legend {
    flex-wrap: wrap;
    gap: 16px;
  }

  .chart-svg {
    width: 100%;
    height: auto;
  }
}

/* Animation effects */
@keyframes fadeInUp {
  from {
    opacity: 0;
    transform: translateY(20px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.chart-container {
  animation: fadeInUp 0.6s ease-out;
}

.legend-item {
  animation: fadeInUp 0.6s ease-out;
}

.legend-item:nth-child(2) {
  animation-delay: 0.1s;
}

.legend-item:nth-child(3) {
  animation-delay: 0.2s;
}
</style>
