import { index, route } from '@react-router/dev/routes';

export default [
  route('sign-in/sso-callback', 'routes/sign-in.sso-callback.jsx'),
  route('sign-up/sso-callback', 'routes/sign-up.sso-callback.jsx'),
  route('sign-in', 'routes/sign-in.jsx'),
  route('sign-up', 'routes/sign-up.jsx'),
  route('sign-out', 'routes/sign-out.jsx'),
  route('auth/session', 'routes/auth.session.jsx'),
  route('auth/dev-login', 'routes/auth.dev-login.jsx'),
  route('auth/dev-logout', 'routes/auth.dev-logout.jsx'),
  route('auth/device', 'routes/auth.device.jsx'),
  route('auth/device/approve', 'routes/auth.device-approve.jsx'),
  route('auth/*', 'routes/auth.$.jsx'),
  route('v1/*', 'routes/v1.$.jsx'),
  index('routes/app-shell.jsx'),
  route('*', 'routes/catchall.jsx'),
];
