import { useState } from 'react';
import { Card, Input, Button, Typography, Banner, Space } from '@douyinfe/semi-ui';
import { IconLock } from '@douyinfe/semi-icons';
import { login, setupPassword, setSession } from '../api/client';

interface Props {
  /** 首次运行：需要设置管理密码 */
  needsSetup: boolean;
  onSuccess: () => void;
}

const { Title, Paragraph, Text } = Typography;

/** 认证页：首次运行设置密码，之后登录 */
export default function Auth({ needsSetup, onSuccess }: Props) {
  const [pwd, setPwd] = useState('');
  const [confirm, setConfirm] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    setError('');

    if (!pwd) {
      setError('请输入密码');
      return;
    }
    if (needsSetup) {
      if (pwd.length < 6) {
        setError('密码至少 6 位');
        return;
      }
      if (pwd !== confirm) {
        setError('两次输入的密码不一致');
        return;
      }
    }

    setLoading(true);
    try {
      const res = needsSetup ? await setupPassword(pwd, confirm) : await login(pwd);
      setSession(res.session);
      setPwd('');
      setConfirm('');
      onSuccess();
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fpkm-login-wrap">
      <Card className="fpkm-login-card" title={null}>
        <Space vertical align="start" spacing={12} style={{ width: '100%' }}>
          <div className="fpkm-logo">
            <img className="fpkm-logo-mark" src="/fpkm-logo.png" alt="FPKM" />
            <span>FPKM 应用商店</span>
          </div>

          {needsSetup ? (
            <>
              <Title heading={5} style={{ margin: 0 }}>
                设置管理密码
              </Title>
              <Paragraph type="tertiary" style={{ marginBottom: 0 }}>
                首次使用需要设置一个管理密码。该密码用于登录本界面，
                以 PBKDF2-HMAC-SHA256 加盐存储，不会明文保存。
              </Paragraph>
            </>
          ) : (
            <>
              <Title heading={5} style={{ margin: 0 }}>
                登录
              </Title>
              <Paragraph type="tertiary" style={{ marginBottom: 0 }}>
                请输入管理密码进入应用商店。
              </Paragraph>
            </>
          )}

          <Input
            mode="password"
            prefix={<IconLock />}
            placeholder={needsSetup ? '设置密码（至少 6 位）' : '管理密码'}
            value={pwd}
            onChange={setPwd}
            onEnterPress={needsSetup ? undefined : submit}
            size="large"
            autoFocus
          />

          {needsSetup ? (
            <Input
              mode="password"
              prefix={<IconLock />}
              placeholder="再次输入密码"
              value={confirm}
              onChange={setConfirm}
              onEnterPress={submit}
              size="large"
            />
          ) : null}

          {error ? <Banner type="danger" description={error} closeIcon={null} /> : null}

          <Button
            theme="solid"
            type="primary"
            block
            size="large"
            loading={loading}
            onClick={submit}
          >
            {needsSetup ? '设置并进入' : '登录'}
          </Button>

          {needsSetup ? (
            <Text type="tertiary" size="small">
              忘记密码时可在服务器执行：fpkm web --reset-password
            </Text>
          ) : null}
        </Space>
      </Card>
    </div>
  );
}
