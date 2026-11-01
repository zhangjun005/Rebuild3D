/**
 * 极简控制面板（纯 DOM，不引框架）。
 *
 * 面板只做三件事：改参数 → 通知 Viewer → 同步控件显示。
 * 参数对象由 Viewer 持有并原地 Object.assign，这里直接改同一个对象。
 */

import type { NumericParamKey, PresetKey, RenderParams } from './params';
import { PRESETS, PRESET_KEYS } from './params';
import type { ViewerStats } from './viewer';
import type { GenParams } from './params';
import { GenSection } from './gen-section';

/** PNG 导出的尺寸选项（只出图片，不做 GIF/视频） */
export type PngSizeKey = 'viewport' | 'square' | '1080p';

export interface ExportRequest {
  sizeKey: PngSizeKey;
}

const PNG_SIZE_OPTIONS: ReadonlyArray<{ key: PngSizeKey; label: string }> = [
  { key: 'viewport', label: '当前视口' },
  { key: 'square', label: '1080×1080 方图' },
  { key: '1080p', label: '1920×1080' },
];

interface SliderDef {
  key: NumericParamKey;
  label: string;
  min: number;
  max: number;
  step: number;
  digits: number;
  /** 只有这个参数 > above 时才显示（半调相关参数在点画模式下藏起来，面板别太长） */
  dependsOn?: { key: NumericParamKey; above: number };
  /** 更一般的显示条件（模式相关：图像模式没有点，点大小/密度要藏起来） */
  visibleWhen?: (params: RenderParams) => boolean;
}

/** 下拉选项（模式 / 墨量来源 / 主体极性这类枚举参数） */
interface SelectDef {
  key: 'renderMode' | 'inkSource' | 'inkPolarity';
  label: string;
  options: ReadonlyArray<{ value: number | string; label: string }>;
  visibleWhen?: (params: RenderParams) => boolean;
}

/** 顺序即面板顺序：L1 → L2 → L4 */
const SLIDERS: readonly SliderDef[] = [
  { key: 'pointSize', label: '点大小', min: 0.5, max: 12, step: 0.1, digits: 1, visibleWhen: (p) => p.renderMode === 'cloud' },
  { key: 'density', label: '点数密度', min: 0.05, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.renderMode === 'cloud' },
  { key: 'stippleScale', label: '点画尺度 px', min: 1.5, max: 10, step: 0.1, digits: 1, visibleWhen: (p) => p.renderMode === 'image' && p.halftone <= 0.001 && p.engraving <= 0.001 },
  { key: 'inkGain', label: '结构增益', min: 1, max: 24, step: 0.5, digits: 1, visibleWhen: (p) => p.colorMode < 0.5 && p.inkSource === 1 },
  { key: 'stipple', label: '点画强度', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.colorMode < 0.5 && p.halftone <= 0.001 && p.engraving <= 0.001 },
  { key: 'densityGamma', label: '明暗曲线', min: 0.4, max: 2.5, step: 0.05, digits: 2, visibleWhen: (p) => p.colorMode < 0.5 },
  { key: 'edgeStrength', label: '描边强度', min: 0, max: 2, step: 0.05, digits: 2 },
  { key: 'edgeThreshold', label: '描边阈值', min: 0.02, max: 0.6, step: 0.01, digits: 2 },
  { key: 'edgeWidth', label: '描边宽度', min: 0.5, max: 4, step: 0.1, digits: 1 },
  { key: 'fogAmount', label: '空气透视', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.renderMode === 'cloud' && p.colorMode < 0.5 },
  { key: 'mono', label: '单色墨点', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.colorMode < 0.5 },
  { key: 'grain', label: '纸颗粒', min: 0, max: 0.3, step: 0.005, digits: 3 },
  { key: 'vignette', label: '暗角', min: 0, max: 1, step: 0.01, digits: 2 },
  { key: 'swayAmplitude', label: '摆动幅度 ±°', min: 0, max: 45, step: 1, digits: 0, visibleWhen: (p) => !p.parallaxMode && p.renderMode === 'cloud' },
  { key: 'parallaxAmount', label: '视差幅度 %', min: 0, max: 15, step: 0.5, digits: 1, visibleWhen: (p) => p.parallaxMode && p.renderMode === 'cloud' },
  { key: 'halftone', label: '半调网点', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.engraving <= 0.001 },
  { key: 'halftoneCell', label: '网点大小 px', min: 3, max: 14, step: 0.5, digits: 1, dependsOn: { key: 'halftone', above: 0.001 } },
  { key: 'halftoneAngle', label: '网屏角度 °', min: 0, max: 90, step: 1, digits: 0, dependsOn: { key: 'halftone', above: 0.001 } },
  { key: 'engraving', label: '铜版画排线', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.halftone <= 0.001 },
  { key: 'hatchLayers', label: '排线层数', min: 2, max: 4, step: 1, digits: 0, dependsOn: { key: 'engraving', above: 0.001 } },
  { key: 'hatchSpacing', label: '排线间距 px', min: 3, max: 12, step: 0.5, digits: 1, dependsOn: { key: 'engraving', above: 0.001 } },
  { key: 'hatchAngle', label: '排线角度 °', min: 0, max: 180, step: 1, digits: 0, dependsOn: { key: 'engraving', above: 0.001 } },
  { key: 'posterizeLevels', label: '色阶数', min: 3, max: 5, step: 1, digits: 0, visibleWhen: (p) => p.colorMode > 0.5 || p.engraving > 0.001 },
  { key: 'colorPosterize', label: '色调分离级数', min: 2, max: 8, step: 1, digits: 0, visibleWhen: (p) => p.colorMode > 0.5 },
  { key: 'colorHalftone', label: '彩色网点叠加', min: 0, max: 1, step: 0.01, digits: 2, visibleWhen: (p) => p.colorMode > 0.5 },
  { key: 'colorSaturation', label: '饱和度增益', min: 0.5, max: 2, step: 0.01, digits: 2, visibleWhen: (p) => p.colorMode > 0.5 },
];

/** 枚举参数（模式 / 墨量来源 / 主体极性）：下拉框比滑块直观得多 */
const SELECTS: readonly SelectDef[] = [

  {
    key: 'inkSource',
    label: '墨量来源',
    options: [
      { value: 1, label: '按局部结构（默认）' },
      { value: 0, label: '按亮度' },
      { value: 2, label: '按深度' },
    ],
  },
  {
    key: 'inkPolarity',
    label: '主体极性',
    options: [
      { value: 0, label: '自动' },
      { value: 1, label: '亮主体' },
      { value: 2, label: '暗主体' },
    ],
  },
];

export interface PanelOptions {
  container: HTMLElement;
  hud: HTMLElement;
  params: RenderParams;
  activePreset: PresetKey;
  onChange: () => void;
  onPreset: (key: PresetKey) => void;
  onExport: (request: ExportRequest) => void;
  /** 用户选了插画文件（→ POST /api/infer） */
  onUpload: (file: File) => void;
  /** 回到内置 fixture 点云 */
  onLoadSample: () => void;
  /** 回正：相机恢复基准位姿 */
  onReset: () => void;
  /** 点云生成参数（不是渲染参数；改动需重新请求 /api/infer） */
  genParams: GenParams;
  /** 点「重新生成点云」 */
  onGenerate: () => void;
}

export class Panel {
  private readonly params: RenderParams;
  private readonly onChange: () => void;
  private readonly onExport: (request: ExportRequest) => void;
  private readonly onResetAction: () => void;
  /** 点云生成参数分组 */
  private readonly genSection: GenSection;
  private readonly syncers: Array<() => void> = [];
  private readonly hud: HTMLElement;
  private readonly container: HTMLElement;
  private readonly presetButtons = new Map<PresetKey, HTMLButtonElement>();
  private sourceLabel = '';
  /** 图像模式（对照）的来源标签：上传的文件名，不含点数 */
  private imageSourceLabel = '';
  private lastStats: ViewerStats | null = null;
  /** 当前预设（导出文件名要用） */
  private preset: PresetKey;
  /** 输出区控件（PNG 导出） */
  private sizeSelect: HTMLSelectElement | null = null;
  private statusElement: HTMLElement | null = null;
  /** 输入区控件（file input 供自动化 / 重复选择用） */
  uploadInput: HTMLInputElement | null = null;
  private inputStatusElement: HTMLElement | null = null;

  constructor(options: PanelOptions) {
    this.params = options.params;
    this.onChange = options.onChange;
    this.onExport = options.onExport;
    this.onResetAction = options.onReset;
    this.hud = options.hud;
    this.container = options.container;
    this.preset = options.activePreset;

    const panel = this.container;
    panel.textContent = '';

    const title = document.createElement('h1');
    title.textContent = 'ILLUST3D · 点画印刷';
    const subtitle = document.createElement('small');
    subtitle.textContent = '\u8f6f\u7247 + \u70b9\u753b/\u534a\u8c03 + \u6df1\u5ea6\u63cf\u8fb9 + \u6cb8\u817e + \u7a7a\u6c14\u900f\u89c6 \u00b7 H \u6536\u8d77';
    title.append(subtitle);
    panel.append(title);

    // 参数区可滚动、导出区钉在底部（参数多，不能把导出按钮挤出去）
    const scrollArea = document.createElement('div');
    scrollArea.className = 'panel-scroll';

    // ── 输入（真实插画 → /api/infer）──
    scrollArea.append(this.buildInputSection(options));

    // ── 渲染模式（segmented，常显）──
    // 产品路径 = 点云（上传即生成）；图像模式是可选的对照/回退。
    // 不藏进 debug 折叠区：用户必须随时知道自己在哪个模式。
    const modeRow = document.createElement('div');
    modeRow.className = 'segmented';
    const modeButtons: Array<{ value: RenderParams['renderMode']; button: HTMLButtonElement }> = [];
    for (const option of [
      { value: 'cloud' as const, label: '点云（产品）' },
      { value: 'image' as const, label: '图像（对照）' },
    ]) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = option.label;
      button.addEventListener('click', () => {
        this.params.renderMode = option.value;
        this.sync();
        this.onChange();
      });
      modeButtons.push({ value: option.value, button });
      modeRow.append(button);
    }
    this.syncers.push(() => {
      for (const entry of modeButtons) {
        entry.button.classList.toggle('active', this.params.renderMode === entry.value);
      }
      this.renderHud();
    });
    scrollArea.append(modeRow);


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
    scrollArea.append(presets);
    this.setActivePreset(options.activePreset);

    // ── 点云生成参数（焦距/点数档位/边缘阈值 + 重新生成）──

    this.genSection = new GenSection({
      params: options.genParams,
      onGenerate: options.onGenerate,
    });
    scrollArea.append(this.genSection.element());


    // ── 墨量映射（第二层：这幅画该怎么读；两种模式共用）──
    for (const def of SELECTS) {
      scrollArea.append(this.buildSelect(def));
    }
    scrollArea.append(document.createElement('hr'));

    // ── 滑块（两列排布：控制项多，单列在 900px 高度下会滚）──
    const sliderGrid = document.createElement('div');
    sliderGrid.className = 'grid2';
    for (const def of SLIDERS) {
      sliderGrid.append(this.buildSlider(def));
    }
    scrollArea.append(sliderGrid);

    scrollArea.append(document.createElement('hr'));

    // ── 开关 ──
    scrollArea.append(this.buildToggle('colorMode', '保留原色（彩色点画）'));
    scrollArea.append(this.buildToggle('boil', '沸腾线（每 2~3 帧换相位）'));
    const motionGrid = document.createElement('div');
    motionGrid.className = 'grid2';
    motionGrid.append(
      this.buildToggle('autoSway', '自动运动（往返）'),
      this.buildToggle('parallaxMode', '视差平移（自动）'),
      this.buildToggle('freeRotate', '自由旋转（手动）'),
    );
    scrollArea.append(motionGrid);

    // ── 背景色（两列）──
    const colorGrid = document.createElement('div');
    colorGrid.className = 'grid2';
    colorGrid.append(this.buildColor('paper', '背景 / 纸色'), this.buildColor('ink', '墨色'));
    scrollArea.append(colorGrid);

    // ── 调试（非产品路径）────────────────────────────────────────────────────
    const debugGroup = document.createElement('details');
    debugGroup.className = 'debug';
    const debugSummary = document.createElement('summary');
    debugSummary.textContent = '调试（非产品路径）';
    debugGroup.append(debugSummary);
    const threeQuarterRow = this.buildToggle('threeQuarterView', '3/4 基准视角（仅调试 fixture）');
    this.syncers.push(() => {
      threeQuarterRow.hidden = this.params.renderMode !== 'cloud';
    });
    debugGroup.append(threeQuarterRow);
    scrollArea.append(debugGroup);

    panel.append(scrollArea);
    panel.append(document.createElement('hr'));

    // ── 输出：只导出 PNG ──
    panel.append(this.buildOutputSection());

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

    // 依赖其它参数 / 当前模式的滑块：不适用时藏起来（面板本来就长）
    const dependsOn = def.dependsOn;
    const visibleWhen = def.visibleWhen;
    if (dependsOn || visibleWhen) {
      this.syncers.push(() => {
        const byParam = dependsOn ? this.params[dependsOn.key] > dependsOn.above : true;
        row.hidden = !(byParam && (visibleWhen ? visibleWhen(this.params) : true));
      });
    }

    return row;
  }

  /** 下拉行（模式 / 墨量来源 / 主体极性） */
  private buildSelect(def: SelectDef): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row';

    const label = document.createElement('label');
    const name = document.createElement('span');
    name.textContent = def.label;
    label.append(name);

    const select = document.createElement('select');
    for (const option of def.options) {
      const el = document.createElement('option');
      el.value = String(option.value);
      el.textContent = option.label;
      select.append(el);
    }
    select.addEventListener('change', () => {
      const raw = def.key === 'renderMode' ? select.value : Number(select.value);
      // 枚举参数和字符串参数分开写，类型才收得住
      if (def.key === 'renderMode') {
        this.params.renderMode = raw as RenderParams['renderMode'];
      } else {
        this.params[def.key] = Number(raw);
      }
      this.onChange();
    });

    row.append(label, select);

    this.syncers.push(() => {
      const value = String(this.params[def.key]);
      if (select.value !== value) select.value = value;
      row.hidden = def.visibleWhen ? !def.visibleWhen(this.params) : false;
    });

    return row;
  }

  private buildToggle(
    key: 'boil' | 'autoSway' | 'threeQuarterView' | 'parallaxMode' | 'colorMode' | 'freeRotate',
    labelText: string,
  ): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row check';

    const input = document.createElement('input');
    input.type = 'checkbox';
    input.id = `toggle-${key}`;

    const label = document.createElement('label');
    label.htmlFor = input.id;
    label.textContent = labelText;

    input.addEventListener('change', () => {
      // colorMode 是 0/1 的数值参数（方便脚本 --param colorMode=0 传），其余开关是布尔
      if (key === 'colorMode') {
        this.params.colorMode = input.checked ? 1 : 0;
      } else {
        this.params[key] = input.checked;
      }
      this.onChange();
    });

    row.append(input, label);
    this.syncers.push(() => {
      input.checked = key === 'colorMode' ? this.params.colorMode > 0.5 : this.params[key];
    });
    return row;
  }

  private buildColor(key: 'paper' | 'ink', labelText: string): HTMLElement {
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

  // ── 输入区（真实插画 → POST /api/infer）──────────────────────────────────
  private buildInputSection(options: PanelOptions): HTMLElement {
    const section = document.createElement('div');
    section.className = 'input-section';

    const heading = document.createElement('div');
    heading.className = 'export-title';
    heading.textContent = '插画输入';
    section.append(heading);

    const buttons = document.createElement('div');
    buttons.className = 'export-buttons';

    const pick = document.createElement('button');
    pick.type = 'button';
    pick.textContent = '选择插画…';
    pick.title = '上传插画（png / jpeg / gif）→ 深度 → 点云';

    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'image/png,image/jpeg,image/gif';
    input.hidden = true;
    input.addEventListener('change', () => {
      const file = input.files?.[0];
      if (file) options.onUpload(file);
      input.value = ''; // 允许重复选同一个文件
    });
    pick.addEventListener('click', () => input.click());

    const sample = document.createElement('button');
    sample.type = 'button';
    sample.textContent = '样例点云';
    sample.title = '回到内置 fixture（testdata/sample.ilcl）';
    sample.addEventListener('click', () => options.onLoadSample());

    buttons.append(pick, sample);
    section.append(buttons, input);

    const status = document.createElement('div');
    status.className = 'input-status';
    status.textContent = '拖一张插画到窗口也行';
    section.append(status);

    this.uploadInput = input;
    this.inputStatusElement = status;
    return section;
  }

  /** 输入区状态文字（加载中/推理中/结果） */
  setInputStatus(text: string): void {
    if (this.inputStatusElement) this.inputStatusElement.textContent = text;
  }

  /** 推理期间禁用面板控件，避免中途改参数/重复上传 */
  setInputBusy(busy: boolean): void {
    this.container.classList.toggle('busy', busy);
    const controls = this.container.querySelectorAll<
      HTMLInputElement | HTMLSelectElement | HTMLButtonElement
    >('input, select, button');
    for (const element of controls) {
      element.disabled = busy;
    }
  }

  // ── 输出区：只导出 PNG（不做 GIF/视频）──────────────────────────────────
  private buildOutputSection(): HTMLElement {
    const section = document.createElement('div');
    section.className = 'export';

    // 回正（钉在底部，永远可见；用户要求：不限角度，但要能一键转回来）
    const resetRow = document.createElement('div');
    resetRow.className = 'row';
    const resetButton = document.createElement('button');
    resetButton.type = 'button';
    resetButton.className = 'wide';
    resetButton.textContent = '回正（回到正面基准）';
    resetButton.title = '把相机恢复到进入时的基准位姿（正面 / 原距离）';
    resetButton.addEventListener('click', () => this.onResetAction());
    resetRow.append(resetButton);
    section.append(resetRow);

    const sizeRow = document.createElement('div');
    sizeRow.className = 'row select';
    const sizeLabel = document.createElement('span');
    sizeLabel.textContent = '导出尺寸';
    const sizeSelect = document.createElement('select');
    for (const option of PNG_SIZE_OPTIONS) {
      const element = document.createElement('option');
      element.value = option.key;
      element.textContent = option.label;
      sizeSelect.append(element);
    }
    sizeRow.append(sizeLabel, sizeSelect);
    section.append(sizeRow);
    this.sizeSelect = sizeSelect;

    const buttons = document.createElement('div');
    buttons.className = 'export-buttons';
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = '导出 PNG';
    button.title = '把当前画面导出成 PNG（文件名带预设与关键参数）';
    button.addEventListener('click', () => {
      this.onExport({ sizeKey: sizeSelect.value as PngSizeKey });
    });
    buttons.append(button);
    section.append(buttons);

    const status = document.createElement('div');
    status.className = 'export-status';
    status.textContent = '';
    this.statusElement = status;
    section.append(status);

    return section;
  }

  /** 当前预设（导出文件名用） */
  /** 外部切换预设（比如真实插画上传后默认用铜版画）：同步按钮高亮 */
  setPreset(key: PresetKey): void {
    this.setActivePreset(key);
    this.sync();
  }

  get currentPreset(): PresetKey {
    return this.preset;
  }

  /** 导出尺寸选项，给自动化脚本读 */
  get exportSizeKey(): PngSizeKey {
    return (this.sizeSelect?.value ?? 'viewport') as PngSizeKey;
  }

  /** 导出中：禁用所有控件，防止导到一半参数被改 */
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

  /** 图像模式（对照）的来源：上传的文件名（不含点数，避免 HUD 自相矛盾） */
  /** 生成参数分组：启用/禁用（上传后才能生成） */
  setGenEnabled(hasFile: boolean): void {
    this.genSection.setEnabled(hasFile);
  }

  /** 生成成功：按钮回到「重新生成」 */
  markGenApplied(): void {
    this.genSection.markApplied();
  }

  /** 生成中：禁用生成按钮 */
  setGenBusy(busy: boolean): void {
    this.genSection.setBusy(busy);
  }

  setImageSource(label: string): void {
    this.imageSourceLabel = label;
    this.renderHud();
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
    const imageMode = this.params.renderMode === 'image';
    const parts: string[] = [];
    if (s) {
      parts.push(`FPS ${s.fps.toFixed(0)}`);
      parts.push(`帧 ${s.frame}`);
      // 图像模式没有点云 → 绝不显示"118,401 点"这种与画面矛盾的信息
      if (imageMode) {
        parts.push('图像模式（对照）');
      } else {
        parts.push(`${s.points.toLocaleString('en-US')} 点`);
      }
      parts.push(`${s.width}×${s.height}`);
      if (!imageMode && s.boilPhase > 0) parts.push(`沸腾相 ${s.boilPhase}`);
    } else {
      parts.push('FPS --');
      parts.push('等待点云…');
    }
    const head = parts.join(' · ');
    // 「源」必须反映真实来源：图像模式 = 上传的插画（不写点数）；点云模式 = 上传插画 / 样例点云
    const source = imageMode
      ? `\n源 ${this.imageSourceLabel || '上传的插画'} · 图像模式（原图，无点云）`
      : this.sourceLabel
        ? `\n源 ${this.sourceLabel}`
        : '';
    this.hud.textContent = head + source;
  }
}
