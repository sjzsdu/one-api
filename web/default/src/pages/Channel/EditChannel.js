import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Form, Input, Message } from 'semantic-ui-react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  API,
  copy,
  getChannelModels,
  showError,
  showInfo,
  showSuccess,
  verifyJSON,
} from '../../helpers';
import { CHANNEL_OPTIONS } from '../../constants';
import { renderChannelTip } from '../../helpers/render';

const MODEL_MAPPING_EXAMPLE = {
  'gpt-3.5-turbo-0301': 'gpt-3.5-turbo',
  'gpt-4-0314': 'gpt-4',
  'gpt-4-32k-0314': 'gpt-4-32k',
};

const OPENAI_CODEX_OAUTH_CHANNEL_TYPE = 52;
const OPENAI_CODEX_FALLBACK_MODELS = [
  'gpt-5.5',
  'gpt-5.4',
  'gpt-5.4-mini',
  'gpt-5.4-nano',
];

function normalizeModelList(models = []) {
  if (!Array.isArray(models)) return [];
  const seen = new Set();
  return models
    .map((model) => String(model).trim())
    .filter((model) => {
      if (model === '' || seen.has(model)) {
        return false;
      }
      seen.add(model);
      return true;
    });
}

function buildModelOptions(models = []) {
  return normalizeModelList(models).map((model) => ({
    key: model,
    text: model,
    value: model,
  }));
}

function isOpenAICodexOAuthType(type) {
  return Number(type) === OPENAI_CODEX_OAUTH_CHANNEL_TYPE;
}

function getInitialChannelModels(type) {
  if (isOpenAICodexOAuthType(type)) {
    return OPENAI_CODEX_FALLBACK_MODELS;
  }
  return normalizeModelList(getChannelModels(type));
}

function type2secretPrompt(type, t) {
  switch (type) {
    case 15:
      return t('channel.edit.key_prompts.zhipu');
    case 18:
      return t('channel.edit.key_prompts.spark');
    case 22:
      return t('channel.edit.key_prompts.fastgpt');
    case 23:
      return t('channel.edit.key_prompts.tencent');
    default:
      return t('channel.edit.key_prompts.default');
  }
}

const EditChannel = () => {
  const { t } = useTranslation();
  const params = useParams();
  const navigate = useNavigate();
  const channelId = params.id;
  const isEdit = channelId !== undefined;
  const [loading, setLoading] = useState(isEdit);
  const handleCancel = () => {
    navigate('/channel');
  };

  const originInputs = {
    name: '',
    type: 1,
    key: '',
    base_url: '',
    other: '',
    model_mapping: '',
    system_prompt: '',
    models: [],
    groups: ['default'],
  };
  const [batch, setBatch] = useState(false);
  const [inputs, setInputs] = useState(originInputs);
  const [originModelOptions, setOriginModelOptions] = useState([]);
  const [modelOptions, setModelOptions] = useState([]);
  const [groupOptions, setGroupOptions] = useState([]);
  const [basicModels, setBasicModels] = useState([]);
  const [fullModels, setFullModels] = useState([]);
  const [customModel, setCustomModel] = useState('');
  const [refreshingModels, setRefreshingModels] = useState(false);
  const [openAIOAuthLoading, setOpenAIOAuthLoading] = useState('');
  const [openAIOAuthNotice, setOpenAIOAuthNotice] = useState('');
  const [config, setConfig] = useState({
    region: '',
    sk: '',
    ak: '',
    user_id: '',
    vertex_ai_project_id: '',
    vertex_ai_adc: '',
  });
  const handleInputChange = (e, { name, value }) => {
    if (name === 'type') {
      const localModels = getInitialChannelModels(value);
      if (isOpenAICodexOAuthType(value)) {
        setFullModels([]);
      }
      setBasicModels(localModels);
      setInputs((inputs) => ({
        ...inputs,
        type: value,
        models:
          isOpenAICodexOAuthType(value) || inputs.models.length === 0
            ? localModels
            : normalizeModelList(inputs.models),
      }));
      return;
    }
    if (name === 'models') {
      value = normalizeModelList(value);
    }
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  };

  const handleConfigChange = (e, { name, value }) => {
    setConfig((inputs) => ({ ...inputs, [name]: value }));
  };

  const loadChannel = async () => {
    let res = await API.get(`/api/channel/${channelId}`);
    const { success, message, data } = res.data;
    if (success) {
      if (data.models === '') {
        data.models = [];
      } else {
        data.models = normalizeModelList(data.models.split(','));
      }
      if (data.group === '') {
        data.groups = [];
      } else {
        data.groups = data.group.split(',');
      }
      if (data.model_mapping !== '') {
        data.model_mapping = JSON.stringify(
          JSON.parse(data.model_mapping),
          null,
          2
        );
      }
      setInputs(data);
      if (data.config !== '') {
        setConfig(JSON.parse(data.config));
      }
      setBasicModels(getInitialChannelModels(data.type));
    } else {
      showError(message);
    }
    setLoading(false);
  };

  const fetchModels = async () => {
    try {
      let res = await API.get(`/api/channel/models`);
      const modelIds = normalizeModelList(
        res.data.data.map((model) => model.id)
      );
      setOriginModelOptions(buildModelOptions(modelIds));
      setFullModels(modelIds);
    } catch (error) {
      showError(error.message);
    }
  };

  const fetchGroups = async () => {
    try {
      let res = await API.get(`/api/group/`);
      setGroupOptions(
        res.data.data.map((group) => ({
          key: group,
          text: group,
          value: group,
        }))
      );
    } catch (error) {
      showError(error.message);
    }
  };

  useEffect(() => {
    const optionMap = new Map();
    const sourceOptions = isOpenAICodexOAuthType(inputs.type)
      ? buildModelOptions(basicModels)
      : originModelOptions;
    sourceOptions.forEach((option) => {
      if (option.value) {
        optionMap.set(option.value, option);
      }
    });
    normalizeModelList(inputs.models).forEach((model) => {
      if (!optionMap.has(model)) {
        optionMap.set(model, {
          key: model,
          text: model,
          value: model,
        });
      }
    });
    setModelOptions(Array.from(optionMap.values()));
  }, [basicModels, originModelOptions, inputs.models, inputs.type]);

  useEffect(() => {
    if (isEdit) {
      loadChannel().then();
    } else {
      let localModels = getInitialChannelModels(inputs.type);
      setBasicModels(localModels);
    }
    fetchModels().then();
    fetchGroups().then();
  }, []);

  const submit = async () => {
    if (inputs.key === '') {
      if (config.ak !== '' && config.sk !== '' && config.region !== '') {
        inputs.key = `${config.ak}|${config.sk}|${config.region}`;
      } else if (
        config.region !== '' &&
        config.vertex_ai_project_id !== '' &&
        config.vertex_ai_adc !== ''
      ) {
        inputs.key = `${config.region}|${config.vertex_ai_project_id}|${config.vertex_ai_adc}`;
      }
    }
    if (!isEdit && inputs.name === '') {
      showInfo(t('channel.edit.messages.name_required'));
      return;
    }
    if (!isEdit && inputs.key === '') {
      showInfo(
        inputs.type === OPENAI_CODEX_OAUTH_CHANNEL_TYPE
          ? t('channel.edit.messages.openai_oauth_required')
          : t('channel.edit.messages.key_required')
      );
      return;
    }
    if (inputs.type !== 43 && inputs.models.length === 0) {
      showInfo(t('channel.edit.messages.models_required'));
      return;
    }
    if (inputs.model_mapping !== '' && !verifyJSON(inputs.model_mapping)) {
      showInfo(t('channel.edit.messages.model_mapping_invalid'));
      return;
    }
    let localInputs = { ...inputs };
    if (localInputs.key === 'undefined|undefined|undefined') {
      localInputs.key = ''; // prevent potential bug
    }
    if (localInputs.base_url && localInputs.base_url.endsWith('/')) {
      localInputs.base_url = localInputs.base_url.slice(
        0,
        localInputs.base_url.length - 1
      );
    }
    if (localInputs.type === 3 && localInputs.other === '') {
      localInputs.other = '2024-03-01-preview';
    }
    let res;
    localInputs.models = normalizeModelList(localInputs.models).join(',');
    localInputs.group = localInputs.groups.join(',');
    localInputs.config = JSON.stringify(config);
    if (isEdit) {
      res = await API.put(`/api/channel/`, {
        ...localInputs,
        id: parseInt(channelId),
      });
    } else {
      res = await API.post(`/api/channel/`, localInputs);
    }
    const { success, message } = res.data;
    if (success) {
      if (isEdit) {
        showSuccess(t('channel.edit.messages.update_success'));
      } else {
        showSuccess(t('channel.edit.messages.create_success'));
        setInputs(originInputs);
      }
    } else {
      showError(message);
    }
  };

  const addCustomModel = () => {
    const model = customModel.trim();
    if (model === '') return;
    if (inputs.models.includes(model)) return;
    const localModels = normalizeModelList([...inputs.models, model]);
    setCustomModel('');
    handleInputChange(null, { name: 'models', value: localModels });
  };

  const addModelOption = (model) => {
    model = String(model || '').trim();
    if (model === '') return;
    setModelOptions((modelOptions) => {
      if (modelOptions.some((option) => option.value === model)) {
        return modelOptions;
      }
      return [
        ...modelOptions,
        {
          key: model,
          text: model,
          value: model,
        },
      ];
    });
    handleInputChange(null, {
      name: 'models',
      value: normalizeModelList([...inputs.models, model]),
    });
  };

  const requestChannelModels = async (key = inputs.key) => {
    const res = await API.post('/api/channel/models/refresh', {
      id: isEdit ? parseInt(channelId) : 0,
      type: inputs.type,
      key,
      base_url: inputs.base_url,
      other: inputs.other,
      config: JSON.stringify(config),
    });
    if (!res || !res.data) {
      throw new Error(t('channel.edit.messages.openai_oauth_request_failed'));
    }
    const { success, message, data } = res.data;
    if (!success) {
      throw new Error(
        message || t('channel.edit.messages.openai_oauth_request_failed')
      );
    }
    return normalizeModelList(data?.models);
  };

  const applyRefreshedModels = (refreshedModels) => {
    setOriginModelOptions((originModelOptions) =>
      buildModelOptions([
        ...originModelOptions.map((option) => option.value),
        ...refreshedModels,
      ])
    );
    setBasicModels(refreshedModels);
    setFullModels(refreshedModels);
    handleInputChange(null, { name: 'models', value: refreshedModels });
  };

  const applyCodexFallbackModels = () => {
    const fallbackModels = normalizeModelList(OPENAI_CODEX_FALLBACK_MODELS);
    setOriginModelOptions((originModelOptions) =>
      buildModelOptions([
        ...originModelOptions.map((option) => option.value),
        ...fallbackModels,
      ])
    );
    setBasicModels(fallbackModels);
    setFullModels(fallbackModels);
    if (normalizeModelList(inputs.models).length === 0) {
      handleInputChange(null, { name: 'models', value: fallbackModels });
    }
  };

  const refreshModels = async () => {
    if (!isEdit && inputs.key.trim() === '') {
      showInfo(
        inputs.type === OPENAI_CODEX_OAUTH_CHANNEL_TYPE
          ? t('channel.edit.messages.openai_oauth_required')
          : t('channel.edit.messages.key_required')
      );
      return;
    }
    setRefreshingModels(true);
    try {
      const refreshedModels = await requestChannelModels();
      applyRefreshedModels(refreshedModels);
      showSuccess(
        t('channel.edit.messages.refresh_models_success', {
          count: refreshedModels.length,
        })
      );
    } catch (error) {
      const message = getOpenAIOAuthErrorMessage(error);
      if (
        isOpenAICodexOAuthType(inputs.type) &&
        isCodexModelListUnavailable(message)
      ) {
        showCodexModelListUnavailableNotice();
        setRefreshingModels(false);
        return;
      }
      if (!error?.isAxiosError) {
        showError(message);
      }
    }
    setRefreshingModels(false);
  };

  const getOpenAIOAuthErrorMessage = (error) => {
    if (!error) {
      return t('channel.edit.messages.openai_oauth_request_failed');
    }
    return (
      error?.response?.data?.message ||
      error?.message ||
      String(error) ||
      t('channel.edit.messages.openai_oauth_request_failed')
    );
  };

  const isCodexModelListUnavailable = (message) =>
    String(message || '').includes('无法自动获取 Codex OAuth 模型列表');

  const showCodexModelListUnavailableNotice = () => {
    applyCodexFallbackModels();
    const message = t(
      'channel.edit.messages.openai_oauth_model_list_unavailable'
    );
    setOpenAIOAuthNotice(message);
    showInfo(message);
  };

  const waitForOpenAIOAuthFlow = async (flowId, method, interval = 2) => {
    const maxAttempts = Math.ceil((15 * 60) / Math.max(interval, 1));
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      await new Promise((resolve) =>
        setTimeout(resolve, Math.max(interval, 1) * 1000)
      );
      const res =
        method === 'device_code'
          ? await API.post(`/api/oauth/openai/flows/${flowId}/poll`)
          : await API.get(`/api/oauth/openai/flows/${flowId}`);
      if (!res || !res.data) {
        throw new Error(t('channel.edit.messages.openai_oauth_request_failed'));
      }
      const { success, message, data } = res.data;
      if (!success) {
        throw new Error(
          message || t('channel.edit.messages.openai_oauth_request_failed')
        );
      }
      if (!data) {
        throw new Error(t('channel.edit.messages.openai_oauth_request_failed'));
      }
      if (data.status === 'success') {
        return data.credential;
      }
      if (data.status === 'error' || data.status === 'expired') {
        throw new Error(data.error || data.status);
      }
    }
    throw new Error(t('channel.edit.messages.openai_oauth_timeout'));
  };

  const applyOpenAIOAuthCredential = async (credential) => {
    if (!credential) {
      throw new Error(t('channel.edit.messages.openai_oauth_empty'));
    }
    handleInputChange(null, { name: 'key', value: credential });
    setBasicModels([]);
    setFullModels([]);
    handleInputChange(null, { name: 'models', value: [] });
    try {
      const refreshedModels = await requestChannelModels(credential);
      applyRefreshedModels(refreshedModels);
      const message = t(
        'channel.edit.messages.openai_oauth_success_with_models',
        {
          count: refreshedModels.length,
        }
      );
      setOpenAIOAuthNotice(message);
      showSuccess(message);
    } catch (error) {
      const errorMessage = getOpenAIOAuthErrorMessage(error);
      if (
        isOpenAICodexOAuthType(inputs.type) &&
        isCodexModelListUnavailable(errorMessage)
      ) {
        showCodexModelListUnavailableNotice();
        return;
      }
      const message = t('channel.edit.messages.openai_oauth_refresh_failed', {
        message: errorMessage,
      });
      setOpenAIOAuthNotice(message);
      if (!error?.isAxiosError) {
        showError(message);
      }
    }
  };

  const startOpenAIOAuth = async (method) => {
    setOpenAIOAuthLoading(method);
    setOpenAIOAuthNotice('');
    const authWindow = window.open(
      'about:blank',
      '_blank',
      method === 'browser' ? 'width=520,height=760' : undefined
    );
    try {
      const res = await API.post('/api/oauth/openai/login', { method });
      if (!res || !res.data) {
        throw new Error(t('channel.edit.messages.openai_oauth_request_failed'));
      }
      const { success, message, data } = res.data;
      if (!success || !data) {
        if (authWindow) {
          authWindow.close();
        }
        showError(
          message || t('channel.edit.messages.openai_oauth_request_failed')
        );
        return;
      }
      if (method === 'browser') {
        if (authWindow) {
          authWindow.location.href = data.auth_url;
        } else {
          window.open(data.auth_url, '_blank', 'width=520,height=760');
        }
        setOpenAIOAuthNotice(t('channel.edit.messages.openai_oauth_waiting'));
        const credential = await waitForOpenAIOAuthFlow(
          data.flow_id,
          method,
          2
        );
        await applyOpenAIOAuthCredential(credential);
      } else {
        if (authWindow) {
          authWindow.location.href = data.verify_url;
        } else {
          window.open(data.verify_url, '_blank');
        }
        setOpenAIOAuthNotice(
          t('channel.edit.messages.openai_oauth_device_code', {
            code: data.user_code,
          })
        );
        const credential = await waitForOpenAIOAuthFlow(
          data.flow_id,
          method,
          data.interval || 5
        );
        await applyOpenAIOAuthCredential(credential);
      }
    } catch (error) {
      const message = getOpenAIOAuthErrorMessage(error);
      if (!error?.isAxiosError) {
        showError(message);
      }
      setOpenAIOAuthNotice(message);
    } finally {
      setOpenAIOAuthLoading('');
    }
  };

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header className='header'>
            {isEdit
              ? t('channel.edit.title_edit')
              : t('channel.edit.title_create')}
          </Card.Header>
          <Form loading={loading} autoComplete='new-password'>
            <Form.Field>
              <Form.Select
                label={t('channel.edit.type')}
                name='type'
                required
                search
                options={CHANNEL_OPTIONS}
                value={inputs.type}
                onChange={handleInputChange}
              />
            </Form.Field>
            <Form.Field>
              <Form.Input
                label={t('channel.edit.name')}
                name='name'
                placeholder={t('channel.edit.name_placeholder')}
                onChange={handleInputChange}
                value={inputs.name}
                required
              />
            </Form.Field>
            <Form.Field>
              <Form.Dropdown
                label={t('channel.edit.group')}
                placeholder={t('channel.edit.group_placeholder')}
                name='groups'
                required
                fluid
                multiple
                selection
                allowAdditions
                additionLabel={t('channel.edit.group_addition')}
                onChange={handleInputChange}
                value={inputs.groups}
                autoComplete='new-password'
                options={groupOptions}
              />
            </Form.Field>
            {renderChannelTip(inputs.type)}

            {/* Azure OpenAI specific fields */}
            {inputs.type === 3 && (
              <>
                <Message>
                  注意，<strong>模型部署名称必须和模型名称保持一致</strong>
                  ，因为 One API 会把请求体中的 model
                  参数替换为你的部署名称（模型名称中的点会被剔除），
                  <a
                    target='_blank'
                    href='https://github.com/songquanpeng/one-api/issues/133?notification_referrer_id=NT_kwDOAmJSYrM2NjIwMzI3NDgyOjM5OTk4MDUw#issuecomment-1571602271'
                  >
                    图片演示
                  </a>
                  。
                </Message>
                <Form.Field>
                  <Form.Input
                    label='AZURE_OPENAI_ENDPOINT'
                    name='base_url'
                    placeholder='请输入 AZURE_OPENAI_ENDPOINT，例如：https://docs-test-001.openai.azure.com'
                    onChange={handleInputChange}
                    value={inputs.base_url}
                    autoComplete='new-password'
                  />
                </Form.Field>
                <Form.Field>
                  <Form.Input
                    label='默认 API 版本'
                    name='other'
                    placeholder='请输入默认 API 版本，例如：2024-03-01-preview，该配置可以被实际的请求查询参数所覆盖'
                    onChange={handleInputChange}
                    value={inputs.other}
                    autoComplete='new-password'
                  />
                </Form.Field>
              </>
            )}

            {/* Custom base URL field */}
            {inputs.type === 8 && (
              <Form.Field>
                <Form.Input
                  required
                  label={t('channel.edit.proxy_url')}
                  name='base_url'
                  placeholder={t('channel.edit.proxy_url_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.base_url}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            {inputs.type === 50 && (
              <Form.Field>
                <Form.Input
                  required
                  label={t('channel.edit.base_url')}
                  name='base_url'
                  placeholder={t('channel.edit.base_url_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.base_url}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}

            {inputs.type === 18 && (
              <Form.Field>
                <Form.Input
                  label={t('channel.edit.spark_version')}
                  name='other'
                  placeholder={t('channel.edit.spark_version_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.other}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            {inputs.type === 21 && (
              <Form.Field>
                <Form.Input
                  label={t('channel.edit.knowledge_id')}
                  name='other'
                  placeholder={t('channel.edit.knowledge_id_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.other}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            {inputs.type === 17 && (
              <Form.Field>
                <Form.Input
                  label={t('channel.edit.plugin_param')}
                  name='other'
                  placeholder={t('channel.edit.plugin_param_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.other}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            {inputs.type === 34 && (
              <Message>{t('channel.edit.coze_notice')}</Message>
            )}
            {inputs.type === 40 && (
              <Message>
                {t('channel.edit.douban_notice')}
                <a
                  target='_blank'
                  href='https://console.volcengine.com/ark/region:ark+cn-beijing/endpoint'
                >
                  {t('channel.edit.douban_notice_link')}
                </a>
                {t('channel.edit.douban_notice_2')}
              </Message>
            )}
            {inputs.type === OPENAI_CODEX_OAUTH_CHANNEL_TYPE && (
              <Form.Field>
                <label>{t('channel.edit.openai_oauth.title')}</label>
                <div style={{ lineHeight: '40px', marginBottom: '12px' }}>
                  <Button
                    type='button'
                    loading={openAIOAuthLoading === 'browser'}
                    disabled={openAIOAuthLoading !== ''}
                    onClick={() => startOpenAIOAuth('browser')}
                  >
                    {t('channel.edit.openai_oauth.browser')}
                  </Button>
                  <Button
                    type='button'
                    loading={openAIOAuthLoading === 'device_code'}
                    disabled={openAIOAuthLoading !== ''}
                    onClick={() => startOpenAIOAuth('device_code')}
                  >
                    {t('channel.edit.openai_oauth.device_code')}
                  </Button>
                </div>
                {openAIOAuthNotice && (
                  <Message info>{openAIOAuthNotice}</Message>
                )}
              </Form.Field>
            )}
            {inputs.type !== 43 && (
              <Form.Field>
                <Form.Dropdown
                  label={t('channel.edit.models')}
                  placeholder={
                    isOpenAICodexOAuthType(inputs.type)
                      ? t('channel.edit.openai_oauth.models_placeholder')
                      : t('channel.edit.models_placeholder')
                  }
                  name='models'
                  required
                  fluid
                  multiple
                  search
                  allowAdditions
                  additionLabel={t('channel.edit.models_addition')}
                  noResultsMessage={t('channel.edit.models_no_results')}
                  onAddItem={(e, { value }) => addModelOption(value)}
                  onLabelClick={(e, { value }) => {
                    copy(value).then();
                  }}
                  selection
                  onChange={handleInputChange}
                  value={inputs.models}
                  autoComplete='new-password'
                  options={modelOptions}
                />
              </Form.Field>
            )}
            {inputs.type !== 43 && (
              <div style={{ lineHeight: '40px', marginBottom: '12px' }}>
                <Button
                  type={'button'}
                  loading={refreshingModels}
                  disabled={refreshingModels}
                  onClick={refreshModels}
                >
                  {t('channel.edit.buttons.refresh_models')}
                </Button>
                <Button
                  type={'button'}
                  onClick={() => {
                    handleInputChange(null, {
                      name: 'models',
                      value: basicModels,
                    });
                  }}
                >
                  {t('channel.edit.buttons.fill_models')}
                </Button>
                <Button
                  type={'button'}
                  onClick={() => {
                    handleInputChange(null, {
                      name: 'models',
                      value: isOpenAICodexOAuthType(inputs.type)
                        ? basicModels
                        : fullModels,
                    });
                  }}
                >
                  {t('channel.edit.buttons.fill_all')}
                </Button>
                <Button
                  type={'button'}
                  onClick={() => {
                    handleInputChange(null, { name: 'models', value: [] });
                  }}
                >
                  {t('channel.edit.buttons.clear')}
                </Button>
                <Input
                  action={
                    <Button type={'button'} onClick={addCustomModel}>
                      {t('channel.edit.buttons.add_custom')}
                    </Button>
                  }
                  placeholder={t('channel.edit.buttons.custom_placeholder')}
                  value={customModel}
                  onChange={(e, { value }) => {
                    setCustomModel(value);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      addCustomModel();
                      e.preventDefault();
                    }
                  }}
                />
              </div>
            )}
            {inputs.type !== 43 && (
              <>
                <Form.Field>
                  <Form.TextArea
                    label={t('channel.edit.model_mapping')}
                    placeholder={`${t(
                      'channel.edit.model_mapping_placeholder'
                    )}\n${JSON.stringify(MODEL_MAPPING_EXAMPLE, null, 2)}`}
                    name='model_mapping'
                    onChange={handleInputChange}
                    value={inputs.model_mapping}
                    style={{
                      minHeight: 150,
                      fontFamily: 'JetBrains Mono, Consolas',
                    }}
                    autoComplete='new-password'
                  />
                </Form.Field>
                <Form.Field>
                  <Form.TextArea
                    label={t('channel.edit.system_prompt')}
                    placeholder={t('channel.edit.system_prompt_placeholder')}
                    name='system_prompt'
                    onChange={handleInputChange}
                    value={inputs.system_prompt}
                    style={{
                      minHeight: 150,
                      fontFamily: 'JetBrains Mono, Consolas',
                    }}
                    autoComplete='new-password'
                  />
                </Form.Field>
              </>
            )}
            {inputs.type === 33 && (
              <Form.Field>
                <Form.Input
                  label='Region'
                  name='region'
                  required
                  placeholder={t('channel.edit.aws_region_placeholder')}
                  onChange={handleConfigChange}
                  value={config.region}
                  autoComplete=''
                />
                <Form.Input
                  label='AK'
                  name='ak'
                  required
                  placeholder={t('channel.edit.aws_ak_placeholder')}
                  onChange={handleConfigChange}
                  value={config.ak}
                  autoComplete=''
                />
                <Form.Input
                  label='SK'
                  name='sk'
                  required
                  placeholder={t('channel.edit.aws_sk_placeholder')}
                  onChange={handleConfigChange}
                  value={config.sk}
                  autoComplete=''
                />
              </Form.Field>
            )}
            {inputs.type === 42 && (
              <Form.Field>
                <Form.Input
                  label='Region'
                  name='region'
                  required
                  placeholder={t('channel.edit.vertex_region_placeholder')}
                  onChange={handleConfigChange}
                  value={config.region}
                  autoComplete=''
                />
                <Form.Input
                  label={t('channel.edit.vertex_project_id')}
                  name='vertex_ai_project_id'
                  required
                  placeholder={t('channel.edit.vertex_project_id_placeholder')}
                  onChange={handleConfigChange}
                  value={config.vertex_ai_project_id}
                  autoComplete=''
                />
                <Form.Input
                  label={t('channel.edit.vertex_credentials')}
                  name='vertex_ai_adc'
                  required
                  placeholder={t('channel.edit.vertex_credentials_placeholder')}
                  onChange={handleConfigChange}
                  value={config.vertex_ai_adc}
                  autoComplete=''
                />
              </Form.Field>
            )}
            {inputs.type === 34 && (
              <Form.Input
                label={t('channel.edit.user_id')}
                name='user_id'
                required
                placeholder={t('channel.edit.user_id_placeholder')}
                onChange={handleConfigChange}
                value={config.user_id}
                autoComplete=''
              />
            )}
            {inputs.type !== 33 &&
              inputs.type !== 42 &&
              inputs.type !== OPENAI_CODEX_OAUTH_CHANNEL_TYPE &&
              (batch ? (
                <Form.Field>
                  <Form.TextArea
                    label={t('channel.edit.key')}
                    name='key'
                    required
                    placeholder={t('channel.edit.batch_placeholder')}
                    onChange={handleInputChange}
                    value={inputs.key}
                    style={{
                      minHeight: 150,
                      fontFamily: 'JetBrains Mono, Consolas',
                    }}
                    autoComplete='new-password'
                  />
                </Form.Field>
              ) : (
                <Form.Field>
                  <Form.Input
                    label={t('channel.edit.key')}
                    name='key'
                    required
                    placeholder={type2secretPrompt(inputs.type, t)}
                    onChange={handleInputChange}
                    value={inputs.key}
                    autoComplete='new-password'
                  />
                </Form.Field>
              ))}
            {inputs.type === OPENAI_CODEX_OAUTH_CHANNEL_TYPE && (
              <Form.Field>
                <Form.TextArea
                  label={t('channel.edit.openai_oauth.credential')}
                  name='key'
                  required
                  placeholder={t(
                    'channel.edit.openai_oauth.credential_placeholder'
                  )}
                  onChange={handleInputChange}
                  value={inputs.key}
                  style={{
                    minHeight: 120,
                    fontFamily: 'JetBrains Mono, Consolas',
                  }}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            {inputs.type === 37 && (
              <Form.Field>
                <Form.Input
                  label='Account ID'
                  name='user_id'
                  required
                  placeholder={
                    '请输入 Account ID，例如：d8d7c61dbc334c32d3ced580e4bf42b4'
                  }
                  onChange={handleConfigChange}
                  value={config.user_id}
                  autoComplete=''
                />
              </Form.Field>
            )}
            {inputs.type !== 33 &&
              inputs.type !== OPENAI_CODEX_OAUTH_CHANNEL_TYPE &&
              !isEdit && (
                <Form.Checkbox
                  checked={batch}
                  label={t('channel.edit.batch')}
                  name='batch'
                  onChange={() => setBatch(!batch)}
                />
              )}
            {inputs.type !== 3 &&
              inputs.type !== 33 &&
              inputs.type !== 8 &&
              inputs.type !== 50 &&
              inputs.type !== OPENAI_CODEX_OAUTH_CHANNEL_TYPE &&
              inputs.type !== 22 && (
                <Form.Field>
                  <Form.Input
                    label={t('channel.edit.proxy_url')}
                    name='base_url'
                    placeholder={t('channel.edit.proxy_url_placeholder')}
                    onChange={handleInputChange}
                    value={inputs.base_url}
                    autoComplete='new-password'
                  />
                </Form.Field>
              )}
            {inputs.type === 22 && (
              <Form.Field>
                <Form.Input
                  label='私有部署地址'
                  name='base_url'
                  placeholder={
                    '请输入私有部署地址，格式为：https://fastgpt.run/api/openapi'
                  }
                  onChange={handleInputChange}
                  value={inputs.base_url}
                  autoComplete='new-password'
                />
              </Form.Field>
            )}
            <Button onClick={handleCancel}>
              {t('channel.edit.buttons.cancel')}
            </Button>
            <Button
              type={isEdit ? 'button' : 'submit'}
              positive
              onClick={submit}
            >
              {t('channel.edit.buttons.submit')}
            </Button>
          </Form>
        </Card.Content>
      </Card>
    </div>
  );
};

export default EditChannel;
