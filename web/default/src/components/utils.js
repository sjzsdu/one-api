import { API, showError } from '../helpers';

export async function getOAuthState() {
  const res = await API.get('/api/oauth/state');
  const { success, message, data } = res.data;
  if (success) {
    return data;
  } else {
    showError(message);
    return '';
  }
}

export async function onGitHubOAuthClicked(github_client_id) {
  const state = await getOAuthState();
  if (!state) return;
  window.open(
    `https://github.com/login/oauth/authorize?client_id=${github_client_id}&state=${state}&scope=user:email`
  );
}

export async function onLarkOAuthClicked(lark_client_id) {
  const state = await getOAuthState();
  if (!state) return;
  let redirect_uri = `${window.location.origin}/oauth/lark`;
  window.open(
    `https://open.feishu.cn/open-apis/authen/v1/index?redirect_uri=${redirect_uri}&app_id=${lark_client_id}&state=${state}`
  );
}

export function getOneAPIServerAddress(serverAddress = '') {
  if (!serverAddress) {
    const status = localStorage.getItem('status');
    if (status) {
      try {
        serverAddress = JSON.parse(status).server_address;
      } catch (e) {
        serverAddress = '';
      }
    }
  }
  if (!serverAddress) {
    serverAddress = window.location.origin;
  }
  return serverAddress.replace(/\/+$/, '');
}

export function buildOpenAICompatibleClientUrl(type, key, serverAddress = '') {
  const normalizedServerAddress = getOneAPIServerAddress(serverAddress);
  const apiKey = key.startsWith('sk-') ? key : `sk-${key}`;
  const rawKey = apiKey.replace(/^sk-/, '');
  const encodedServerAddress = encodeURIComponent(normalizedServerAddress);
  const chatLink = localStorage.getItem('chat_link');
  const nextBase = chatLink || 'https://app.nextchat.dev';

  switch (type) {
    case 'ama':
      return `ama://set-api-key?server=${encodedServerAddress}&key=${apiKey}`;
    case 'opencat':
      return `opencat://team/join?domain=${encodedServerAddress}&token=${apiKey}`;
    case 'next':
      return `${nextBase}/#/?settings={"key":"${apiKey}","url":"${normalizedServerAddress}"}`;
    case 'lobechat':
      if (!chatLink) {
        return apiKey;
      }
      return `${chatLink}/?settings={"keyVaults":{"openai":{"apiKey":"${apiKey}","baseURL":"${normalizedServerAddress}/v1"}}}`;
    default:
      return type === 'raw' ? apiKey : `sk-${rawKey}`;
  }
}
