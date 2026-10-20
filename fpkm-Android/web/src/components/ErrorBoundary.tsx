import { Component, type ErrorInfo, type ReactNode } from 'react';
import { Button, Card, Typography, Banner } from '@douyinfe/semi-ui';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

const { Paragraph, Title } = Typography;

/**
 * 顶层错误边界。
 * 单个组件抛错时展示可恢复的错误页，而不是让整个界面变成白屏，
 * 同时保留错误详情便于排查。
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // 输出到控制台，便于在浏览器中定位
    console.error('[fpkm] 界面渲染出错:', error, info.componentStack);
  }

  private reset = () => {
    this.setState({ error: null });
  };

  private reload = () => {
    window.location.reload();
  };

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <div className="fpkm-login-wrap">
        <Card className="fpkm-login-card" style={{ maxWidth: 640 }}>
          <Title heading={4} style={{ marginTop: 0 }}>
            界面出现异常
          </Title>
          <Paragraph type="tertiary">
            某个组件渲染失败。可以尝试重试；若持续出现，请把下方错误信息反馈给开发者。
          </Paragraph>
          <Banner
            type="danger"
            closeIcon={null}
            description={<span className="fpkm-mono">{error.message}</span>}
            style={{ marginBottom: 16 }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <Button theme="solid" type="primary" onClick={this.reset}>
              重试
            </Button>
            <Button onClick={this.reload}>重新加载页面</Button>
          </div>
        </Card>
      </div>
    );
  }
}
