import { useEffect, useMemo, useState } from 'react';
import { ShieldCheck, Trash2 } from 'lucide-react';

import { deleteAdminUserByEmail, fetchAdminStatus } from '../utils/api.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';

export default function AdminPage({ initialIsAdmin = false } = {}) {
  const [status, setStatus] = useState(() => ({ isAdmin: initialIsAdmin }));
  const [statusError, setStatusError] = useState('');
  const [email, setEmail] = useState('');
  const [confirmEmail, setConfirmEmail] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState(null);
  const [formError, setFormError] = useState('');

  useEffect(() => {
    let cancelled = false;
    fetchAdminStatus()
      .then((payload) => {
        if (!cancelled) {
          setStatus(payload || {});
          setStatusError('');
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setStatusError(error?.message || 'Unable to load admin status.');
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const normalizedEmail = useMemo(() => email.trim().toLowerCase(), [email]);
  const normalizedConfirmation = useMemo(() => confirmEmail.trim().toLowerCase(), [confirmEmail]);
  const canSubmit = Boolean(status?.isAdmin && normalizedEmail && normalizedEmail === normalizedConfirmation && !submitting);

  const handleSubmit = async (event) => {
    event.preventDefault();
    setFormError('');
    setResult(null);
    if (!normalizedEmail.includes('@')) {
      setFormError('Enter a valid email address.');
      return;
    }
    if (normalizedEmail !== normalizedConfirmation) {
      setFormError('Confirm the email address before deleting the user.');
      return;
    }
    setSubmitting(true);
    try {
      const payload = await deleteAdminUserByEmail(normalizedEmail);
      setResult(payload || {});
      setEmail('');
      setConfirmEmail('');
    } catch (error) {
      setFormError(error?.message || 'Unable to delete user.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="admin-page section space-y-4" data-testid="admin-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Administration</Badge>
        <h2>Admin Console</h2>
        <p>Restricted operations for configured admin users.</p>
      </div>

      {statusError && <div className="panel-error" role="alert">{statusError}</div>}
      {!statusError && status?.adminConfigured === false && (
        <div className="panel-error" role="alert">No admin users are configured for this deployment.</div>
      )}
      {!statusError && status?.isAdmin === false && (
        <div className="panel-error" role="alert">Your signed-in email is not allowed to use admin operations.</div>
      )}

      <Card className="admin-card border-border/70">
        <CardHeader>
          <CardTitle className="admin-card-title">
            <ShieldCheck size={20} aria-hidden="true" />
            Delete Staging User
          </CardTitle>
        </CardHeader>
        <CardContent>
          <form className="admin-delete-form" onSubmit={handleSubmit}>
            <label className="admin-field">
              <span>Email</span>
              <Input
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                placeholder="user@example.com"
                autoComplete="off"
                data-testid="admin-delete-email"
                disabled={!status?.isAdmin || submitting}
              />
            </label>
            <label className="admin-field">
              <span>Confirm Email</span>
              <Input
                type="email"
                value={confirmEmail}
                onChange={(event) => setConfirmEmail(event.target.value)}
                placeholder="user@example.com"
                autoComplete="off"
                data-testid="admin-delete-email-confirm"
                disabled={!status?.isAdmin || submitting}
              />
            </label>
            {formError && <div className="panel-error" role="alert">{formError}</div>}
            {result && (
              <div className="panel-success" role="status" data-testid="admin-delete-result">
                Deleted {result.username || normalizedEmail}. Removed {result.deletedSlices || 0} slices, {result.deletedSessions || 0} sessions, and {result.deletedAgentKeys || 0} agent keys.
              </div>
            )}
            <div className="admin-actions">
              <Button
                type="submit"
                variant="destructive"
                disabled={!canSubmit}
                data-testid="admin-delete-submit"
              >
                <Trash2 size={16} aria-hidden="true" />
                {submitting ? 'Deleting...' : 'Delete User'}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </section>
  );
}
