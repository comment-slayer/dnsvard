const API_ORIGIN = 'https://eu.i.posthog.com';
const ASSET_ORIGIN = 'https://eu-assets.i.posthog.com';

const stripPrefix = (pathname) => {
  if (pathname === '/t') {
    return '/';
  }

  if (pathname.startsWith('/t/')) {
    return pathname.slice(2);
  }

  return pathname;
};

const resolveOrigin = (proxiedPath) => {
  if (proxiedPath.startsWith('/static/')) {
    return ASSET_ORIGIN;
  }

  return API_ORIGIN;
};

const resolveCacheControl = (proxiedPath, searchParams, upstreamCacheControl) => {
  if (!proxiedPath.startsWith('/static/')) {
    return upstreamCacheControl;
  }

  if (searchParams.has('v')) {
    return 'public, max-age=31536000, immutable';
  }

  return upstreamCacheControl;
};

export async function onRequest(context) {
  const { request } = context;
  const requestUrl = new URL(request.url);
  const proxiedPath = stripPrefix(requestUrl.pathname);
  const upstreamOrigin = resolveOrigin(proxiedPath);
  const upstreamUrl = new URL(proxiedPath + requestUrl.search, upstreamOrigin);

  const upstreamRequest = new Request(upstreamUrl.toString(), request);
  const upstreamResponse = await fetch(upstreamRequest);
  const headers = new Headers(upstreamResponse.headers);
  const cacheControl = resolveCacheControl(
    proxiedPath,
    requestUrl.searchParams,
    upstreamResponse.headers.get('cache-control')
  );

  if (cacheControl) {
    headers.set('cache-control', cacheControl);
  }

  return new Response(upstreamResponse.body, {
    status: upstreamResponse.status,
    statusText: upstreamResponse.statusText,
    headers,
  });
}
