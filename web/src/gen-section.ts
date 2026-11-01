/**
 * 「点云生成参数」分组：焦距 / 点数档位 / 边缘阈值 / 高级（采样模式、工作分辨率）。
 *
 * ★ 这些**不是渲染参数**：改完必须点「重新生成」重新请求 /api/infer（约 1~3s）。
 *   所以刻意不做成"拖一下就变"，而是维持一份 applied 快照，用来显示"有未应用的改动"。
 */

import type { GenParams } from './params';
import { MAX_POINT_TIERS } from './params';

export interface GenSectionOptions {
  params: GenParams;
  /** 点「重新生成」 */
  onGenerate: () => void;
}

export class GenSection {
  private readonly params: GenParams;
  private readonly onGenerate: () => void;
  private readonly root: HTMLElement;
  private readonly syncers: Array<() => void> = [];
  /** 已应用到点云的参数快照（用于 dirty 判定） */
  private applied: GenParams;
  private generateButton: HTMLButtonElement;
  private hint: HTMLElement;
  private inputs: Array<HTMLInputElement | HTMLButtonElement | HTMLSelectElement> = [];
  private hasFile = false;

  constructor(options: GenSectionOptions) {
    this.params = options.params;
    this.onGenerate = options.onGenerate;
    this.applied = { ...options.params };

    const root = document.createElement('div');
    root.className = 'gen-section';
    this.root = root;

    const heading = document.createElement('div');
    heading.className = 'export-title';
    heading.textContent = '点云生成参数';
    root.append(heading);

    // 焦距 + 边缘阈值（两列，省面板高度）
    const twoCol = document.createElement('div');
    twoCol.className = 'grid2';
    twoCol.append(
      this.slider('焦距系数', 'focalFactor', {
        min: 0.4,
        max: 2.5,
        step: 0.05,
        digits: 2,
        hint: '小=广角（更厚/立体感强）· 大=长焦（更薄/像浮雕）',
      }),
      this.slider('边缘阈值', 'edgeThresh', {
        min: 0,
        max: 1,
        step: 0.01,
        digits: 2,
        hint: '0 = 自动（按深度差 p99）；调大 = 更少剔除边缘点',
      }),
    );
    root.append(twoCol);

    // 点数档位
    const tierRow = document.createElement('div');
    tierRow.className = 'row';
    const tierLabel = document.createElement('label');
    const tierName = document.createElement('span');
    tierName.textContent = '点数档位';
    tierLabel.append(tierName);
    tierRow.append(tierLabel);
    const tierButtons = document.createElement('div');
    tierButtons.className = 'segmented';
    const tierEls: Array<{ value: number; button: HTMLButtonElement }> = [];
    for (const tier of MAX_POINT_TIERS) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = tier.label;
      button.addEventListener('click', () => {
        this.params.maxPoints = tier.value;
        this.sync();
      });
      tierEls.push({ value: tier.value, button });
      tierButtons.append(button);
      this.inputs.push(button);
    }
    tierRow.append(tierButtons);
    root.append(tierRow);
    this.syncers.push(() => {
      for (const entry of tierEls) {
        entry.button.classList.toggle('active', this.params.maxPoints === entry.value);
      }
    });

    // 边缘阈值
    root.append(
      this.slider('边缘阈值', 'edgeThresh', {
        min: 0,
        max: 1,
        step: 0.01,
        digits: 2,
        hint: '0 = 自动（按深度差 p99）；调大 = 更少剔除边缘点',
      }),
    );

    // 高级
    const advanced = document.createElement('details');
    advanced.className = 'debug';
    const summary = document.createElement('summary');
    summary.textContent = '高级';
    advanced.append(summary);

    const modeRow = document.createElement('div');
    modeRow.className = 'row';
    const modeLabel = document.createElement('label');
    const modeName = document.createElement('span');
    modeName.textContent = '采样模式';
    modeLabel.append(modeName);
    const modeSelect = document.createElement('select');
    for (const option of [
      { value: 'structure', label: 'structure（跟随图像结构）' },
      { value: 'depthpixel', label: 'depthpixel（对照基线）' },
    ]) {
      const el = document.createElement('option');
      el.value = option.value;
      el.textContent = option.label;
      modeSelect.append(el);
    }
    modeSelect.addEventListener('change', () => {
      this.params.sampleMode = modeSelect.value as GenParams['sampleMode'];
      this.sync();
    });
    this.inputs.push(modeSelect);
    modeRow.append(modeLabel, modeSelect);
    advanced.append(modeRow);
    this.syncers.push(() => {
      modeSelect.value = this.params.sampleMode;
    });

    const workRow = document.createElement('div');
    workRow.className = 'row';
    const workLabel = document.createElement('label');
    const workName = document.createElement('span');
    workName.textContent = '工作分辨率 px';
    const workOut = document.createElement('output');
    workLabel.append(workName, workOut);
    const workInput = document.createElement('input');
    workInput.type = 'range';
    workInput.min = '0';
    workInput.max = '2048';
    workInput.step = '64';
    workInput.addEventListener('input', () => {
      this.params.workRes = Number(workInput.value);
      workOut.textContent = this.params.workRes === 0 ? '自动' : String(this.params.workRes);
      this.sync();
    });
    this.inputs.push(workInput);
    workRow.append(workLabel, workInput);
    advanced.append(workRow);
    this.syncers.push(() => {
      workInput.value = String(this.params.workRes);
      workOut.textContent = this.params.workRes === 0 ? '自动' : String(this.params.workRes);
    });
    root.append(advanced);

    // 重新生成按钮
    const buttonRow = document.createElement('div');
    buttonRow.className = 'row';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'wide';
    button.textContent = '重新生成';
    button.title = '按当前生成参数重新请求 /api/infer（约 1~3s）';
    button.addEventListener('click', () => this.onGenerate());
    this.generateButton = button;
    buttonRow.append(button);
    root.append(buttonRow);

    const hint = document.createElement('div');
    hint.className = 'input-status';
    hint.textContent = '先上传插画，才能生成点云';
    this.hint = hint;
    root.append(hint);

    this.setEnabled(false);
  }

  element(): HTMLElement {
    return this.root;
  }

  /** 上传成功后：启用本组控件 */
  setEnabled(hasFile: boolean): void {
    this.hasFile = hasFile;
    for (const input of this.inputs) input.disabled = !hasFile;
    this.generateButton.disabled = !hasFile;
    this.hint.textContent = hasFile ? '' : '先上传插画，才能生成点云';
    this.sync();
  }

  /** 生成成功：把当前参数记为"已应用"，按钮回到「重新生成」 */
  markApplied(): void {
    this.applied = { ...this.params };
    this.sync();
  }

  /** 生成中：禁用按钮，防重复点击 */
  setBusy(busy: boolean): void {
    for (const input of this.inputs) input.disabled = busy || !this.hasFile;
    this.generateButton.disabled = busy || !this.hasFile;
    if (busy) this.generateButton.textContent = '生成中…';
    else this.sync();
  }

  /** 刷新控件显示 + dirty 按钮文案 */
  sync(): void {
    for (const sync of this.syncers) sync();
    const dirty = dirtyOf(this.params, this.applied);
    const points = this.params.maxPoints.toLocaleString('en-US');
    this.generateButton.textContent = dirty ? `重新生成点云（${points} 点）` : '重新生成';
    this.generateButton.classList.toggle('dirty', dirty);
  }

  private slider(
    label: string,
    key: 'focalFactor' | 'edgeThresh',
    config: { min: number; max: number; step: number; digits: number; hint: string },
  ): HTMLElement {
    const row = document.createElement('div');
    row.className = 'row';
    const labelEl = document.createElement('label');
    const name = document.createElement('span');
    name.textContent = label;
    name.title = config.hint;
    const output = document.createElement('output');
    labelEl.append(name, output);
    const input = document.createElement('input');
    input.type = 'range';
    input.min = String(config.min);
    input.max = String(config.max);
    input.step = String(config.step);
    input.addEventListener('input', () => {
      this.params[key] = Number(input.value);
      output.textContent = Number(input.value).toFixed(config.digits);
      this.sync();
    });
    this.inputs.push(input);
    row.append(labelEl, input);
    this.syncers.push(() => {
      input.value = String(this.params[key]);
      output.textContent = this.params[key].toFixed(config.digits);
    });
    return row;
  }
}

function dirtyOf(current: GenParams, applied: GenParams): boolean {
  return (
    current.focalFactor !== applied.focalFactor ||
    current.maxPoints !== applied.maxPoints ||
    current.edgeThresh !== applied.edgeThresh ||
    current.sampleMode !== applied.sampleMode ||
    current.workRes !== applied.workRes
  );
}
