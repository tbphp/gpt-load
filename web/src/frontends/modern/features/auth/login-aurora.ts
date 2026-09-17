const vertexSource = `
attribute vec2 aPosition;
void main() {
  gl_Position = vec4(aPosition, 0.0, 1.0);
}
`

// 连续噪声决定光幕的弯折、厚度和细纹，没有固定轨道或首尾循环的关键帧。
// 最后的静态抖色减轻大面积柔光的色阶断层，不随时间闪烁。
const fragmentSource = `
precision highp float;
uniform vec2 uResolution;
uniform vec2 uOffset;
uniform float uTime;
uniform float uMirror;
uniform vec3 uCoral;
uniform vec3 uApricot;

float hash(vec2 p) {
  return fract(sin(p.x * 41.29 + p.y * 97.17) * 25139.731);
}

float noise(vec2 p) {
  vec2 cell = floor(p);
  vec2 local = fract(p);
  local = local * local * (3.0 - 2.0 * local);
  return mix(
    mix(hash(cell), hash(cell + vec2(1.0, 0.0)), local.x),
    mix(hash(cell + vec2(0.0, 1.0)), hash(cell + vec2(1.0, 1.0)), local.x),
    local.y
  );
}

float curtain(vec2 p, float time) {
  float broad = noise(vec2(p.x * 2.4 - time * 0.09, time * 0.13));
  float folds = noise(vec2(p.x * 6.8 + time * 0.11, time * 0.19));
  float ridge = 0.19 + p.x * 0.2 + (broad - 0.5) * 0.28 + (folds - 0.5) * 0.065;
  float thickness = 0.052 + 0.035 * noise(vec2(p.x * 4.0 + time * 0.06, time * 0.1 + 8.0));
  float distance = (p.y - ridge) / thickness;
  float edge = exp(-distance * distance * 2.5);
  float upper = max(distance, 0.0);
  float lower = min(distance, 0.0);
  float veil = exp(-upper * upper * 0.17 - lower * lower * 1.3);
  float strands = 0.82 + 0.18 * noise(vec2(p.x * 75.0 + folds * 6.0, p.y * 3.0 - time * 0.2));
  return edge * 0.25 + veil * strands * 0.45;
}

void main() {
  vec2 uv = gl_FragCoord.xy / uResolution;
  uv.x = mix(uv.x, 1.0 - uv.x, uMirror);
  uv += uOffset;
  float coral = curtain(uv, uTime);
  float apricot = curtain(vec2(1.0 - uv.x, 1.0 - uv.y), uTime * 0.79 + 37.0);
  float silk = curtain(uv + vec2(0.0, 0.07), uTime * 0.63 + 11.0) * 0.18;
  float energy = coral + apricot + silk;
  vec3 color = (
    uCoral * coral + uApricot * apricot + mix(uCoral, uApricot, 0.5) * silk
  ) / max(energy, 0.0001);
  float grain = (hash(gl_FragCoord.xy) - 0.5) * 0.008;
  gl_FragColor = vec4(clamp(color + grain, 0.0, 1.0), min(energy, 0.74));
}
`

export function createLoginAurora(canvas: HTMLCanvasElement) {
  const context = canvas.getContext('webgl', {
    alpha: true,
    antialias: false,
    depth: false,
    stencil: false,
    premultipliedAlpha: false,
    powerPreference: 'low-power',
  })
  if (!context) {
    console.warn('Login aurora: WebGL is unavailable; using the static background.')
    return undefined
  }
  const gl = context
  const program = gl.createProgram()
  const buffer = gl.createBuffer()
  const shaders: WebGLShader[] = []

  function releaseResources(): void {
    gl.useProgram(null)
    gl.bindBuffer(gl.ARRAY_BUFFER, null)
    if (buffer) gl.deleteBuffer(buffer)
    if (program) gl.deleteProgram(program)
    for (const shader of shaders) gl.deleteShader(shader)
    gl.getExtension('WEBGL_lose_context')?.loseContext()
  }

  if (!program || !buffer) {
    releaseResources()
    console.warn('Login aurora: allocation failed; using the static background.')
    return undefined
  }

  try {
    for (const [type, source] of [
      [gl.VERTEX_SHADER, vertexSource],
      [gl.FRAGMENT_SHADER, fragmentSource],
    ] as const) {
      const shader = gl.createShader(type)
      if (!shader) throw new Error('Unable to allocate an aurora shader.')
      shaders.push(shader)
      gl.shaderSource(shader, source)
      gl.compileShader(shader)
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
        throw new Error(gl.getShaderInfoLog(shader) || 'Unable to compile the aurora shader.')
      }
      gl.attachShader(program, shader)
    }
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      throw new Error(gl.getProgramInfoLog(program) || 'Unable to link the aurora shader.')
    }
  } catch (error) {
    releaseResources()
    console.warn('Login aurora: initialization failed; using the static background.', error)
    return undefined
  }

  gl.useProgram(program)
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer)
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW)
  const position = gl.getAttribLocation(program, 'aPosition')
  gl.enableVertexAttribArray(position)
  gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0)
  const resolution = gl.getUniformLocation(program, 'uResolution')
  const offset = gl.getUniformLocation(program, 'uOffset')
  const time = gl.getUniformLocation(program, 'uTime')
  const coral = gl.getUniformLocation(program, 'uCoral')
  const apricot = gl.getUniformLocation(program, 'uApricot')
  gl.uniform2f(resolution, canvas.width, canvas.height)
  gl.uniform1f(gl.getUniformLocation(program, 'uMirror'), Math.random() < 0.5 ? 0 : 1)

  // 初始位置与形态每次不同；速度和轻微偏移在随机目标之间连续过渡。
  // 暂停、主题切换和表单更新都延续当前状态，不重新采样。
  let elapsed = Math.random() * 120 + 10
  const originX = (Math.random() - 0.5) * 0.24
  const originY = (Math.random() - 0.5) * 0.12
  function randomMotion() {
    return {
      speed: (0.88 + Math.random() * 0.24) * 3.4,
      x: originX + (Math.random() - 0.5) * 0.08,
      y: originY + (Math.random() - 0.5) * 0.04,
    }
  }
  let motionStart = randomMotion()
  let motionTarget = randomMotion()
  let motion = motionStart
  let motionElapsed = 0
  let motionDuration = 4 + Math.random() * 4
  let frame = 0
  let lastTime: number | undefined
  let disposed = false
  let contextLost = false
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const colorCanvas = document.createElement('canvas')
  colorCanvas.width = 1
  colorCanvas.height = 1
  const colorContext = colorCanvas.getContext('2d', { willReadFrequently: true })
  if (!colorContext) {
    releaseResources()
    console.warn('Login aurora: theme colors are unavailable; using the static background.')
    return undefined
  }
  const palette = colorContext

  function readColor(element: Element): Float32Array {
    palette.clearRect(0, 0, 1, 1)
    palette.fillStyle = getComputedStyle(element).color
    palette.fillRect(0, 0, 1, 1)
    const pixel = palette.getImageData(0, 0, 1, 1).data
    return new Float32Array([pixel[0]! / 255, pixel[1]! / 255, pixel[2]! / 255])
  }

  function draw(): void {
    if (disposed || contextLost || document.hidden) return
    gl.uniform1f(time, elapsed)
    gl.uniform2f(offset, motion.x, motion.y)
    gl.drawArrays(gl.TRIANGLES, 0, 3)
  }

  function refreshColors(): void {
    if (disposed || contextLost || !canvas.parentElement) return
    gl.uniform3fv(coral, readColor(canvas))
    gl.uniform3fv(apricot, readColor(canvas.parentElement))
    draw()
  }

  function resize(): void {
    if (disposed || contextLost) return
    const width = Math.max(1, canvas.clientWidth)
    const height = Math.max(1, canvas.clientHeight)
    // 光幕是柔和背景，限制绘制分辨率，避免高分屏放大持续渲染成本。
    const scale = Math.min(window.devicePixelRatio || 1, 1.5, 1440 / width, 960 / height)
    canvas.width = Math.max(1, Math.round(width * scale))
    canvas.height = Math.max(1, Math.round(height * scale))
    gl.viewport(0, 0, canvas.width, canvas.height)
    gl.uniform2f(resolution, canvas.width, canvas.height)
    draw()
  }

  function animate(now: number): void {
    frame = 0
    if (disposed || contextLost || document.hidden || reducedMotion.matches) return
    if (lastTime === undefined) lastTime = now
    const delta = now - lastTime
    if (delta >= 1000 / 30) {
      const seconds = Math.min(delta, 100) / 1000
      motionElapsed += seconds
      if (motionElapsed >= motionDuration) {
        motionElapsed -= motionDuration
        motionStart = motionTarget
        motionTarget = randomMotion()
        motionDuration = 4 + Math.random() * 4
      }
      const progress = motionElapsed / motionDuration
      const blend = progress * progress * (3 - 2 * progress)
      motion = {
        speed: motionStart.speed + (motionTarget.speed - motionStart.speed) * blend,
        x: motionStart.x + (motionTarget.x - motionStart.x) * blend,
        y: motionStart.y + (motionTarget.y - motionStart.y) * blend,
      }
      elapsed += seconds * motion.speed
      lastTime = now
      draw()
    }
    frame = requestAnimationFrame(animate)
  }

  function syncMotion(): void {
    cancelAnimationFrame(frame)
    frame = 0
    lastTime = undefined
    if (disposed || contextLost || document.hidden) return
    draw()
    if (!reducedMotion.matches) frame = requestAnimationFrame(animate)
  }

  function loseContext(): void {
    contextLost = true
    cancelAnimationFrame(frame)
    console.warn('Login aurora: graphics context lost; using the static background.')
  }

  refreshColors()
  resize()
  const observer = new ResizeObserver(resize)
  observer.observe(canvas)
  reducedMotion.addEventListener('change', syncMotion)
  document.addEventListener('visibilitychange', syncMotion)
  canvas.addEventListener('webglcontextlost', loseContext)
  syncMotion()

  return {
    refreshColors,
    dispose(): void {
      disposed = true
      cancelAnimationFrame(frame)
      observer.disconnect()
      reducedMotion.removeEventListener('change', syncMotion)
      document.removeEventListener('visibilitychange', syncMotion)
      canvas.removeEventListener('webglcontextlost', loseContext)
      releaseResources()
    },
  }
}
