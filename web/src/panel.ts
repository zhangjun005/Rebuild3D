/**
 * 极简控制面板（纯 DOM，不引框架）。
 *
 * 面板只做三件事：改参数 → 通知 Viewer → 同步控件显示。
 * 参数对象由 Viewer 持有并原地 Object.assign，这里直接改同一个对象。
 */

import type { NumericParamKey, PresetKey, RenderParams } from './params';
import { PRESETS, PRESET_KEYS } from './params';
import type { ViewerStats } from './viewer';
import type { OutputSizeKey } from './capture';

/** 导出的三种动作 */
export type ExportAction = 'record' | 'frames' | 'seam';

export interface ExportRequest {
  action: ExportAction;
  seconds: number;
  sizeKey: OutputSizeKey;
}

/** 时长选项（秒）：30fps × 秒数都能被 3 整除，沸腾相位才能在循环点回绕 */
const DURATIONS = [4, 6, 10] as const;
const DEFAULT_DURATION = 6;
const SIZE_OPTIONS: ReadonlyArray<{ key: OutputSizeKey; label: string }> = [
  { key: 'square', label: '1080×1080 方图' },
  { key: '1080p', label: '1920×1080' },
  { key: 'viewport', label: '当前视口' },
];

interface SliderDef {
  key: NumericParamKey;
  label: string;
  min: number;
  max: number;
  step: number;
  digits: number;
}

/** 顺序即面板顺序：L1 → L2 → L4 */
const SLIDERS: readonly SliderDef[] = [
  { key: 'pointSize', label: '点大小', min: 0.5, max: 12, step: 0.1, digits: 1 },
  { key: 'density', label: '点数密度', min: 0.05, max: 1, step: 0.01, digits: 2 },
  { key: 'stipple', label: '点画强度', min: 0, max: 1, step: 0.01, digits: 2 },
  { key: 'densityGamma', label: '明暗曲线', min: 0.4, max: 2.5, step: 0.05, digits: 2 },
  { key: 'edgeStrength', label: '描边强度', min: 0, max: 2, step: 0.05, digits: 2 },
  { key: 'edgeThreshold', label: '描边阈值', min: 0.02, max: 0.6, step: 0.01, digits: 2 },
  { key: 'edgeWidth', label: '描边宽度', min: 0.5, max: 4, step: 0.1, digits: 1 },
  { key: 'fogAmount', label: '空气透视', min: 0, max: 1, step: 0.01, digits: 2 },
  { key: 'mono', label: '单色墨点', min: 0, max: 1, step: 0.01, digits: 2 },
  { key: 'grain', label: '纸颗粒', min: 0, max: 0.3, step: 0.005, digits: 3 },
  { key: 'vignette', label: '暗角', min: 0, max: 1, step: 0.01, digits: 2 },
];

export interface PanelOptions {
  container: HTMLElement;
  hud: HTMLElement;
  params: RenderParams;
  activePreset: PresetKey;
  onChange: () => void;
  onPreset: (key: PresetKey) => void;
  onExport: (request: ExportRequest) => void;
}

export class Panel {
  private readonly params: RenderParams;
  private readonly onChange: () => void;
  private readonly onExport: (request: ExportRequest) => void;
  private readonly syncers: Array<() => void> = [];
  private readonly hud: HTMLElement;
  private readonly container: HTMLElement;
  private readonly presetButtons = new Map<PresetKey, HTMLButtonElement>();
  private sourceLabel = '';
  private lastStats: ViewerStats | null = null;
  /** 当前预设（导出文件名要用） */
  private preset: PresetKey;
  /** 导出区控件 */
  private durationSelect: HTMLSelectElement | null = null;
  private sizeSelect: HTMLSelectElement | null = null;
  private exportButtons: HTMLButtonElement[] = [];
  private statusElement: HTMLElement | null = null;

  constructor(options: PanelOptions) {
    this.params = options.params;
    this.onChange = options.onChange;
    this.onExport = options.onExport;
    this.hud = options.hud;
    this.container = options.container;
    this.preset = options.activePreset;

    const panel = this.container;
    panel.textContent = '';

    const title = document.createElement('h1');
    title.textContent = 'ILLUST3D · 点画印刷';
    const subtitle = document.createElement('small');
    subtitle.textContent = 'L1 软片 · L2 blue noise 点画 + 深度描边 · L3 沸腾 · L4 空气透视';
    title.append(subtitle);
    panel.append(title);

    // ── 预设 ──
    const presets = document.createElement('div');
    presets.className = 'presets';
    for (const key of PRESET_KEYS) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = PRESETS[key].label;
      button.addEventListener('click', () => {
        options.onPreset(key);
        this.setActivePreset(key);
        this.sync();
      });
      this.presetButtons.set(key, button);
      presets.append(button);
    }
    panel.append(presets);
    this.setActivePreset(options.activePreset);

    // ── 滑块 ──
    for (const def of SLIDERS) {
      panel.append(this.buildSlider(def));
    }

    panel.append(document.createElement('hr'));

    // ── 开关 ──
    panel.append(this.buildToggle('boil', '沸腾线（每 2~3 帧换相位）'));
    panel.append(this.buildToggle('autoRotate', '自动慢速旋转（录动图）'));

    // ── 背景色 ──
    panel.append(this.buildColor('paper', '背景 / 纸色'));

    panel.append(document.createElement('hr'));

    // ── 导出（A 阶段：WebM 循环录制 + 帧序列 + 无缝自检）──
    panel.append(this.buildExportSection());

    panel.append(document.createElement('hr'));

    const hint = document.createElement('div');
    hint.className = 'hint';
    hint.textContent = '拖拽旋转 · 滚轮缩放 · H 键收起面板';
    panel.append(hint);

    this.sync();
    this.renderHud();

    window.addEventListener('keydown', (event) => {
      if (event.key === 'h' || event.key === 'H') {
        panel.classList.toggle('hidden');
      }
    });
  }

  private buildSlider(def: SliderDef): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row';

    const label = document.createElement('label');
    const name = document.createElement('span');
    name.textContent = def.label;
    const output = document.createElement('output');
    label.append(name, output);

    const input = document.createElement('input');
    input.type = 'range';
    input.min = String(def.min);
    input.max = String(def.max);
    input.step = String(def.step);

    input.addEventListener('input', () => {
      this.params[def.key] = Number(input.value);
      output.textContent = Number(input.value).toFixed(def.digits);
      this.onChange();
    });

    row.append(label, input);

    this.syncers.push(() => {
      const value = this.params[def.key];
      input.value = String(value);
      output.textContent = value.toFixed(def.digits);
    });

    return row;
  }

  private buildToggle(key: 'boil' | 'autoRotate', labelText: string): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row check';

    const input = document.createElement('input');
    input.type = 'checkbox';
    input.id = `toggle-${key}`;

    const label = document.createElement('label');
    label.htmlFor = input.id;
    label.textContent = labelText;

    input.addEventListener('change', () => {
      this.params[key] = input.checked;
      this.onChange();
    });

    row.append(input, label);
    this.syncers.push(() => {
      input.checked = this.params[key];
    });
    return row;
  }

  private buildColor(key: 'paper', labelText: string): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row color';

    const label = document.createElement('span');
    label.textContent = labelText;

    const input = document.createElement('input');
    input.type = 'color';

    input.addEventListener('input', () => {
      this.params[key] = input.value;
      this.onChange();
    });

    row.append(label, input);
    this.syncers.push(() => {
      input.value = this.params[key];
    });
    return row;
  }

  // ── 导出区 ──────────────────────────────────────────────────────────────
  private buildExportSection(): HTMLElement {
    const section = document.createElement('div');
    section.className = 'export';

    const heading = document.createElement('div');
    heading.className = 'export-title';
    heading.textContent = '循环动图导出';
    section.append(heading);

    // 时长
    const durationRow = document.createElement('div');
    durationRow.className = 'row select';
    const durationLabel = document.createElement('span');
    durationLabel.textContent = '时长';
    const durationSelect = document.createElement('select');
    for (const seconds of DURATIONS) {
      const option = document.createElement('option');
      option.value = String(seconds);
      option.textContent = `${seconds}s（${seconds * 30} 帧 @30fps）`;
      if (seconds === DEFAULT_DURATION) option.selected = true;
      durationSelect.append(option);
    }
    durationRow.append(durationLabel, durationSelect);
    section.append(durationRow);

    // 尺寸
    const sizeRow = document.createElement('div');
    sizeRow.className = 'row select';
    const sizeLabel = document.createElement('span');
    sizeLabel.textContent = '尺寸';
    const sizeSelect = document.createElement('select');
    for (const option of SIZE_OPTIONS) {
      const element = document.createElement('option');
      element.value = option.key;
      element.textContent = option.label;
      sizeSelect.append(element);
    }
    sizeRow.append(sizeLabel, sizeSelect);
    section.append(sizeRow);

    this.durationSelect = durationSelect;
    this.sizeSelect = sizeSelect;

    // 按钮
    const buttons = document.createElement('div');
    buttons.className = 'export-buttons';
    const specs: Array<{ action: ExportAction; label: string; hint: string }> = [
      { action: 'record', label: '录制 WebM', hint: '录一个完整循环（可循环播放）' },
      { action: 'frames', label: '导出帧序列', hint: '逐帧 PNG，给 Go 侧 image/gif 编码' },
      { action: 'seam', label: '无缝自检', hint: '比较 index 0 与 index N 是否逐像素相同' },
    ];
    for (const spec of specs) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = spec.label;
      button.title = spec.hint;
      button.addEventListener('click', () => {
        this.onExport({
          action: spec.action,
          seconds: Number(durationSelect.value),
          sizeKey: sizeSelect.value as OutputSizeKey,
        });
      });
      this.exportButtons.push(button);
      buttons.append(button);
    }
    section.append(buttons);

    const status = document.createElement('div');
    status.className = 'export-status';
    status.textContent = '';
    this.statusElement = status;
    section.append(status);

    return section;
  }

  /** 当前预设（导出文件名用） */
  get currentPreset(): PresetKey {
    return this.preset;
  }

  /** 导出参数（尺寸/时长），给自动化脚本读 */
  get exportSelection(): { seconds: number; sizeKey: OutputSizeKey } {
    return {
      seconds: Number(this.durationSelect?.value ?? DEFAULT_DURATION),
      sizeKey: (this.sizeSelect?.value ?? 'square') as OutputSizeKey,
    };
  }

  /** 导出中：禁用所有控件，防止录到一半参数被改 */
  setExportBusy(busy: boolean): void {
    this.container.classList.toggle('busy', busy);
    const controls = this.container.querySelectorAll<
      HTMLInputElement | HTMLSelectElement | HTMLButtonElement
    >('input, select, button');
    for (const element of controls) {
      element.disabled = busy;
    }
  }

  setExportStatus(text: string): void {
    if (this.statusElement) this.statusElement.textContent = text;
  }

  private setActivePreset(key: PresetKey): void {
    this.preset = key;
    for (const [presetKey, button] of this.presetButtons) {
      button.classList.toggle('active', presetKey === key);
    }
  }

  /** 把参数值刷回所有控件（换预设后调用） */
  sync(): void {
    for (const sync of this.syncers) sync();
  }

  setSource(label: string): void {
    this.sourceLabel = label;
    this.renderHud();
  }

  updateStats(stats: ViewerStats): void {
    this.lastStats = stats;
    this.renderHud();
  }

  private renderHud(): void {
    const s = this.lastStats;
    const parts: string[] = [];
    if (s) {
      parts.push(`FPS ${s.fps.toFixed(0)}`);
      parts.push(`帧 ${s.frame}`);
      parts.push(`${s.points.toLocaleString('en-US')} 点`);
      parts.push(`${s.width}×${s.height}`);
      if (s.boilPhase > 0) parts.push(`沸腾相 ${s.boilPhase}`);
    } else {
      parts.push('FPS --');
      parts.push('等待点云…');
    }
    const head = parts.join(' · ');
    const source = this.sourceLabel ? `\n源 ${this.sourceLabel}` : '';
    this.hud.textContent = head + source;
  }
}
