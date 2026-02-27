import { useEffect, useMemo, useState } from 'react';
import { fetchEnvironments, getSliceEnvironment, updateSliceEnvironment } from '../utils/api.js';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function SliceSettings({ sliceId, sliceName }) {
  const [environments, setEnvironments] = useState([]);
  const [currentEnvironment, setCurrentEnvironment] = useState('');
  const [selectedEnvironment, setSelectedEnvironment] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    if (!sliceId) {
      setEnvironments([]);
      setCurrentEnvironment('');
      setSelectedEnvironment('');
      setLoading(false);
      return;
    }

    let active = true;

    const load = async () => {
      setLoading(true);
      setError('');
      setSuccess('');
      try {
        const [envList, current] = await Promise.all([
          fetchEnvironments(),
          getSliceEnvironment(sliceId),
        ]);
        if (!active) {
          return;
        }

        const nextEnvironment = current?.environment || '';
        setEnvironments(envList || []);
        setCurrentEnvironment(nextEnvironment);
        setSelectedEnvironment(nextEnvironment);
      } catch (err) {
        if (!active) {
          return;
        }
        setError(err?.message || 'Unable to load slice settings.');
      } finally {
        if (active) {
          setLoading(false);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [sliceId]);

  const sortedEnvironments = useMemo(() => {
    return [...environments].sort((a, b) => {
      const aLabel = (a.displayName || a.name || '').toLowerCase();
      const bLabel = (b.displayName || b.name || '').toLowerCase();
      return aLabel.localeCompare(bLabel);
    });
  }, [environments]);

  const canSave = selectedEnvironment !== '' && selectedEnvironment !== currentEnvironment && !saving;

  const save = async () => {
    if (!sliceId || !canSave) {
      return;
    }

    setSaving(true);
    setError('');
    setSuccess('');
    try {
      const updated = await updateSliceEnvironment(sliceId, selectedEnvironment);
      const nextEnvironment = updated?.environment || selectedEnvironment;
      setCurrentEnvironment(nextEnvironment);
      setSelectedEnvironment(nextEnvironment);
      setSuccess('Environment updated. To persist in your repo, update `.gitslice/config.yaml`.');
    } catch (err) {
      setError(err?.message || 'Unable to save slice environment.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="slice-settings" data-testid="slice-settings-panel">
      <div className="slice-settings-header">
        <h3>Slice settings</h3>
        <p>
          Configure default runtime environment for <strong>{sliceName || sliceId}</strong>.
        </p>
      </div>

      {loading && <div className="panel-empty">Loading settings…</div>}
      {!loading && error && <div className="panel-error">{error}</div>}
      {!loading && success && <div className="panel-success">{success}</div>}

      {!loading && !error && (
        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="slice-settings-form">
              <label htmlFor="slice-settings-environment">Environment</label>
              <select
                id="slice-settings-environment"
                value={selectedEnvironment}
                onChange={(event) => {
                  setSelectedEnvironment(event.target.value);
                  setSuccess('');
                }}
                data-testid="slice-settings-environment-select"
              >
                <option value="" disabled>
                  {sortedEnvironments.length === 0 ? 'No environments available' : 'Select an environment'}
                </option>
                {sortedEnvironments.map((env) => (
                  <option key={env.name} value={env.name}>
                    {env.displayName || env.name}
                  </option>
                ))}
              </select>

              <div className="slice-settings-actions">
                <Button
                  type="button"
                  variant="secondary"
                  className="history-toggle"
                  disabled={!canSave}
                  onClick={save}
                  data-testid="slice-settings-save"
                >
                  {saving ? 'Saving…' : 'Save'}
                </Button>
                {!currentEnvironment && <span className="slice-settings-note">No default environment is set yet.</span>}
              </div>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
