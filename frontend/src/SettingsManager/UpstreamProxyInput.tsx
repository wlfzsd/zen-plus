import { FormGroup, HTMLSelect, InputGroup, NumericInput, Switch, Tooltip } from '@blueprintjs/core';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useDebouncedCallback } from 'use-debounce';

import { AppToaster } from '@/common/toaster';
import { useProxyState } from '@/context/ProxyStateContext';
import { GetUpstreamProxy, SetUpstreamProxy } from 'wails/go/config/Config';
import { config } from 'wails/go/models';

const UPSTREAM_TYPES = ['http', 'https', 'socks5'] as const;

export function UpstreamProxyInput() {
  const { t } = useTranslation();
  const { isProxyRunning } = useProxyState();
  const [upstream, setUpstreamState] = useState<config.UpstreamProxyConfig | null>(null);

  useEffect(() => {
    (async () => {
      setUpstreamState(await GetUpstreamProxy());
    })();
  }, []);

  const loading = upstream === null;
  const disabled = loading || isProxyRunning;
  // The details stay editable only while an upstream is enabled.
  const detailsDisabled = disabled || !upstream?.enabled;

  const save = useDebouncedCallback(async (next: config.UpstreamProxyConfig, previous: config.UpstreamProxyConfig) => {
    try {
      await SetUpstreamProxy(next);
    } catch (err) {
      setUpstreamState(previous);
      AppToaster.show({
        message: t('upstreamProxyInput.saveError', { error: err }),
        intent: 'danger',
      });
    }
  }, 500);

  function update(patch: Partial<config.UpstreamProxyConfig>) {
    if (!upstream) {
      return;
    }
    const next = { ...upstream, ...patch };
    setUpstreamState(next);
    void save(next, upstream);
  }

  return (
    <FormGroup
      label={t('upstreamProxyInput.label')}
      helperText={
        <>
          {t('upstreamProxyInput.description')}
          <br />
          {t('upstreamProxyInput.helper')}
        </>
      }
    >
      <Tooltip content={t('common.stopProxyToModify') as string} disabled={!isProxyRunning} placement="top">
        <div className="settings-manager__upstream-proxy">
          <Switch
            checked={upstream?.enabled ?? false}
            label={t('upstreamProxyInput.enabled')}
            onChange={(event) => {
              update({ enabled: event.currentTarget.checked });
            }}
            disabled={disabled}
          />
          <div className="settings-manager__upstream-proxy-row">
            <HTMLSelect
              value={upstream?.type ?? 'http'}
              onChange={(event) => {
                update({ type: event.currentTarget.value });
              }}
              disabled={detailsDisabled}
              options={UPSTREAM_TYPES.map((value) => ({
                value,
                label: t(`upstreamProxyInput.type${value.charAt(0).toUpperCase()}${value.slice(1)}`) as string,
              }))}
            />
            <InputGroup
              placeholder={t('upstreamProxyInput.host') as string}
              value={upstream?.host ?? ''}
              onValueChange={(host) => {
                update({ host });
              }}
              disabled={detailsDisabled}
              className="settings-manager__upstream-proxy-host"
            />
            <NumericInput
              placeholder={t('upstreamProxyInput.port') as string}
              min={1}
              max={65535}
              buttonPosition="none"
              value={upstream?.port ?? 0}
              onValueChange={(port) => {
                update({ port });
              }}
              disabled={detailsDisabled}
              className="settings-manager__upstream-proxy-port"
            />
          </div>
          <div className="settings-manager__upstream-proxy-row">
            <InputGroup
              placeholder={t('upstreamProxyInput.username') as string}
              value={upstream?.username ?? ''}
              onValueChange={(username) => {
                update({ username });
              }}
              disabled={detailsDisabled}
              leftIcon="user"
            />
            <InputGroup
              placeholder={t('upstreamProxyInput.password') as string}
              value={upstream?.password ?? ''}
              onValueChange={(password) => {
                update({ password });
              }}
              disabled={detailsDisabled}
              type="password"
              leftIcon="lock"
            />
          </div>
        </div>
      </Tooltip>
    </FormGroup>
  );
}
