import { useEffect, useMemo, useState } from 'react';
import {
  Modal,
  Button,
  Typography,
  Space,
  Input,
  Radio,
  RadioGroup,
  Checkbox,
  CheckboxGroup,
  Select,
  Switch,
  Banner,
  Spin,
  Toast,
} from '@douyinfe/semi-ui';
import type { WizardItem, WizardResponse, WizardStep } from '../api/types';
import { useViewport, fitPopupWidth } from '../hooks/useViewport';

const { Text } = Typography;

interface Props {
  visible: boolean;
  /** 向导数据；null 表示加载中 */
  wizard: WizardResponse | null;
  loading?: boolean;
  error?: string;
  /** 标题前缀，如"安装 ScrcpyNas" */
  subject?: string;
  onCancel: () => void;
  /** 校验通过后提交，values 为 field → 值 */
  onSubmit: (values: Record<string, string>) => void;
  submitting?: boolean;
}

/** 校验单个字段，返回错误信息（空字符串表示通过） */
function validateItem(item: WizardItem, value: string): string {
  const rules = item.rules ?? [];
  const val = (value ?? '').trim();

  for (const r of rules) {
    const msg = r.message || '该字段不合法';

    if (r.required && val === '') return msg;
    if (val === '') continue; // 非必填空值跳过其余规则

    const len = [...val].length;

    if (r.len != null && len !== r.len) return msg;
    if (r.min != null && len < r.min) return msg;
    if (r.max != null && len > r.max) return msg;

    if (r.pattern) {
      try {
        // 后端传回的是 JSON 中的字符串，这里按 JS 正则再校验一次
        if (!new RegExp(r.pattern).test(val)) return msg;
      } catch {
        // 正则本身非法时不拦截，交由后端判定
      }
    }
  }
  return '';
}

/**
 * 向导弹窗。
 *
 * 支持多步骤（steps）与 7 种字段类型；tips 类型只渲染富文本提示，不产生值。
 *
 * helpText 在真实应用里普遍内嵌 HTML（<br>、<strong>、<code>、
 * 甚至内联样式），因此按 HTML 渲染。文案来源是本地 FPK 包，
 * 属于用户主动安装的内容，风险与安装该应用本身一致。
 */
export default function WizardModal({
  visible,
  wizard,
  loading,
  error,
  subject,
  onCancel,
  onSubmit,
  submitting,
}: Props) {
  const steps: WizardStep[] = useMemo(() => wizard?.steps ?? [], [wizard]);

  const [stepIndex, setStepIndex] = useState(0);
  const [values, setValues] = useState<Record<string, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});

  // 向导弹窗由 portal 挂到 body，固定 640px 在手机上会溢出屏幕，按视口收敛宽度。
  const vp = useViewport();

  // 打开时用默认值初始化
  useEffect(() => {
    if (!visible) return;
    setStepIndex(0);
    setErrors({});
    const init: Record<string, string> = {};
    for (const s of steps) {
      for (const it of s.items) {
        if (!it.field || it.type === 'tips') continue;
        if (init[it.field] !== undefined) continue;
        init[it.field] =
          wizard?.defaults?.[it.field] ?? it.initValue ?? defaultForType(it);
      }
    }
    setValues(init);
  }, [visible, steps, wizard]);

  const current = steps[stepIndex];
  const isLast = stepIndex >= steps.length - 1;
  const isFirst = stepIndex === 0;

  const setValue = (field: string, v: string) => {
    setValues((prev) => ({ ...prev, [field]: v }));
    // 用户修改后清掉该字段的错误提示
    setErrors((prev) => (prev[field] ? { ...prev, [field]: '' } : prev));
  };

  /** 校验当前步骤 */
  const validateStep = (): boolean => {
    if (!current) return true;
    const next: Record<string, string> = {};
    for (const it of current.items) {
      if (!it.field || it.type === 'tips') continue;
      const msg = validateItem(it, values[it.field] ?? '');
      if (msg) next[it.field] = msg;
    }
    setErrors(next);
    return Object.keys(next).length === 0;
  };

  const next = () => {
    if (!validateStep()) {
      Toast.warning('请检查当前步骤中标记的字段');
      return;
    }
    if (isLast) {
      // 全量再校验一次，避免跨步骤遗漏
      const all: Record<string, string> = {};
      for (const s of steps) {
        for (const it of s.items) {
          if (!it.field || it.type === 'tips') continue;
          const msg = validateItem(it, values[it.field] ?? '');
          if (msg) all[it.field] = msg;
        }
      }
      if (Object.keys(all).length > 0) {
        setErrors(all);
        const firstBad = steps.findIndex((s) =>
          s.items.some((it) => it.field && all[it.field]),
        );
        if (firstBad >= 0) setStepIndex(firstBad);
        Toast.warning('还有字段未通过校验');
        return;
      }
      onSubmit(values);
      return;
    }
    setStepIndex((i) => i + 1);
  };

  const prev = () => setStepIndex((i) => Math.max(0, i - 1));

  const renderItem = (item: WizardItem, tipsKind: 'help' | 'heading' | 'normal' = 'normal') => {
    const val = values[item.field] ?? '';
    const err = errors[item.field];

    switch (item.type) {
      case 'tips': {
        const cls =
          tipsKind === 'help'
            ? 'fpkm-wizard-tips fpkm-wizard-tips-help'
            : tipsKind === 'heading'
              ? 'fpkm-wizard-tips fpkm-wizard-tips-heading'
              : 'fpkm-wizard-tips';
        return (
          <div
            key={item.field || item.helpText.slice(0, 16)}
            className={cls}
            // helpText 来自本地 FPK 包，含 HTML 富文本
            dangerouslySetInnerHTML={{ __html: sanitizeWizardHTML(item.helpText) }}
          />
        );
      }

      case 'password':
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <FieldLabel item={item} />
            <Input
              mode="password"
              value={val}
              placeholder={item.placeholder}
              onChange={(v) => setValue(item.field, v)}
            />
            <FieldError msg={err} />
          </div>
        );

      case 'radio':
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <FieldLabel item={item} />
            <RadioGroup
              value={val}
              onChange={(e) => setValue(item.field, String(e.target.value))}
              direction="vertical"
            >
              {(item.options ?? []).map((o) => (
                <Radio key={o.value} value={o.value}>
                  {o.label}
                </Radio>
              ))}
            </RadioGroup>
            <FieldError msg={err} />
          </div>
        );

      case 'checkbox': {
        // 多选：值以逗号分隔存储，便于作为单个环境变量传递
        const selected = val ? val.split(',').filter(Boolean) : [];
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <FieldLabel item={item} />
            <CheckboxGroup
              value={selected}
              onChange={(list) => setValue(item.field, (list as string[]).join(','))}
              direction="vertical"
            >
              {(item.options ?? []).map((o) => (
                <Checkbox key={o.value} value={o.value}>
                  {o.label}
                </Checkbox>
              ))}
            </CheckboxGroup>
            <FieldError msg={err} />
          </div>
        );
      }

      case 'select':
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <FieldLabel item={item} />
            <Select
              value={val || undefined}
              placeholder={item.placeholder || '请选择'}
              onChange={(v) => setValue(item.field, String(v ?? ''))}
              style={{ width: '100%' }}
            >
              {(item.options ?? []).map((o) => (
                <Select.Option key={o.value} value={o.value}>
                  {o.label}
                </Select.Option>
              ))}
            </Select>
            <FieldError msg={err} />
          </div>
        );

      case 'switch':
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <div className="fpkm-wizard-switch">
              <span style={{ marginBottom: 0 }}>
                <FieldLabel item={item} />
              </span>
              <Switch
                checked={val === 'true' || val === '1'}
                onChange={(v) => setValue(item.field, v ? 'true' : 'false')}
              />
            </div>
            <FieldError msg={err} />
          </div>
        );

      case 'text':
      default:
        return (
          <div key={item.field} className="fpkm-wizard-field">
            <FieldLabel item={item} />
            <Input
              value={val}
              placeholder={item.placeholder}
              onChange={(v) => setValue(item.field, v)}
            />
            <FieldError msg={err} />
          </div>
        );
    }
  };

  // 加载中 / 出错 / 无步骤时，不应显示"下一步/完成"这类误导性按钮
  const ready = !loading && !error && steps.length > 0;

  const footer = ready ? (
    <Space>
      <Button onClick={onCancel} disabled={submitting}>
        取消
      </Button>
      {!isFirst ? (
        <Button onClick={prev} disabled={submitting}>
          上一步
        </Button>
      ) : null}
      <Button theme="solid" type="primary" loading={submitting} onClick={next}>
        {isLast ? '完成' : '下一步'}
      </Button>
    </Space>
  ) : (
    <Button onClick={onCancel} disabled={submitting}>
      取消
    </Button>
  );

  // 标题格式参考官方向导：「安装 指尖NAS - 默认配置设置」
  // 即"动作 + 应用名" + 当前步骤标题；没有步骤标题时退回通用标题
  const stepTitle = current?.stepTitle?.trim();
  const modalTitle = subject
    ? stepTitle
      ? `${subject} - ${stepTitle}`
      : subject
    : stepTitle || wizard?.title || '向导';

  return (
    <Modal
      title={<span>{modalTitle}</span>}
      visible={visible}
      onCancel={submitting ? undefined : onCancel}
      footer={footer}
      width={fitPopupWidth(640, vp)}
      bodyStyle={vp.narrow ? { padding: '14px 14px 18px' } : undefined}
      maskClosable={false}
      closable={!submitting}
    >
      {loading ? (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" />
        </div>
      ) : error ? (
        <Banner type="danger" description={error} closeIcon={null} />
      ) : !steps.length ? (
        <Banner type="info" description="该应用没有配置向导" closeIcon={null} />
      ) : (
        <div className="fpkm-wizard-body">
          <div className="fpkm-wizard-items">
            {current?.items.map((it, idx) => {
              // 依据上下文的 tips 语义，用不同字号/字重形成层级：
              //  - 紧跟输入字段之后 → 字段说明（小字、淡色）
              //  - 紧接输入字段之前 → 小节标题（加粗）
              //    例如指尖NAS:「端口号设置」下面就是 port 输入框
              //  - 其余 → 普通提示段落
              let tipsKind: 'help' | 'heading' | 'normal' = 'normal';
              if (it.type === 'tips') {
                const prev = current.items[idx - 1];
                const next = current.items[idx + 1];
                if (prev && prev.type !== 'tips') tipsKind = 'help';
                else if (next && next.type !== 'tips') tipsKind = 'heading';
              }
              return (
                <div key={it.field || `tips-${idx}`}>{renderItem(it, tipsKind)}</div>
              );
            })}
          </div>
        </div>
      )}
    </Modal>
  );
}

/** 字段标签：加粗显示，必填项带红色星号（与官方向导一致） */
function FieldLabel({ item }: { item: WizardItem }) {
  if (!item.label) return null;
  const required = (item.rules ?? []).some((r) => r.required);
  return (
    <label className="fpkm-wizard-label">
      {item.label}
      {required ? <span className="fpkm-wizard-required"> *</span> : null}
    </label>
  );
}

/** 字段下方的错误提示 */
function FieldError({ msg }: { msg?: string }) {
  if (!msg) return null;
  return (
    <Text type="danger" size="small" className="fpkm-wizard-error">
      {msg}
    </Text>
  );
}

/** 按类型给出默认值 */
function defaultForType(item: WizardItem): string {
  switch (item.type) {
    case 'radio':
    case 'select':
      return item.options?.[0]?.value ?? '';
    case 'switch':
      return 'false';
    case 'checkbox':
      return '';
    default:
      return '';
  }
}

/**
 * 清洗向导 HTML：只保留展示类标签与安全属性。
 *
 * helpText 来自本地 FPK 包，但仍是不可信内容（第三方应用可自行构造）。
 * 这里用白名单过滤掉 script/style/iframe 及所有 on* 事件属性，
 * 避免安装包通过向导文案执行脚本。
 */
function sanitizeWizardHTML(html: string): string {
  if (!html) return '';
  let out = html;

  // 去掉危险标签及其内容
  out = out.replace(/<\s*(script|style|iframe|object|embed|link|meta)\b[\s\S]*?<\s*\/\s*\1\s*>/gi, '');
  // 去掉自闭合形式的危险标签
  out = out.replace(/<\s*(script|iframe|object|embed|link|meta)\b[^>]*\/?>/gi, '');

  // 去掉所有 on* 事件属性
  out = out.replace(/\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, '');

  // 去掉 javascript: / data: 协议
  out = out.replace(/(href|src)\s*=\s*("\s*(javascript|data|vbscript):[^"]*"|'\s*(javascript|data|vbscript):[^']*')/gi, '$1="#"');

  return out;
}
