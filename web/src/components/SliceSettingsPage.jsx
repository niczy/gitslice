import { useEffect, useMemo, useState } from 'react';
import { Eye, FolderTree, KeyRound } from 'lucide-react';

import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceSettings from './SliceSettings.jsx';
import { normalizeVisibility, visibilityLabel } from './settings/SliceSettingsHelpers.js';

function readSettingField(value, camelName, snakeName, fallback = '') {
  return value?.[camelName] ?? value?.[snakeName] ?? fallback;
}

export default function SliceSettingsPage({
  sliceId,
  slices,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
  onOpenAgents,
  initialSettingsData = null,
}) {
  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const currentFolderMounts = currentSlice?.folder_mounts;
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');
  const [folderMounts, setFolderMounts] = useState(() => currentFolderMounts || []);
  const visibilityValue = readSettingField(
    initialSettingsData?.visibility,
    'visibility',
    'visibility',
    currentSlice?.visibility ?? currentSlice?.Visibility ?? '',
  );
  const visibilityText = visibilityValue ? visibilityLabel(normalizeVisibility(visibilityValue)) : 'Private';
  const envEntries = Array.isArray(initialSettingsData?.env?.entries) ? initialSettingsData.env.entries : null;
  const summaryItems = [
    { label: 'Visibility', value: visibilityText, icon: Eye },
    { label: 'Tracked folders', value: String(folderMounts.length), icon: FolderTree },
    { label: 'Environment entries', value: envEntries ? String(envEntries.length) : 'Loading', icon: KeyRound },
  ];

  useEffect(() => {
    setFolderMounts(currentFolderMounts || []);
  }, [currentFolderMounts, sliceId]);

  return (
    <section className="slice-settings-page" data-testid="slice-settings-page">
      <SliceDetailNav
        activeTab="settings"
        sliceId={sliceId}
        sliceLabel={sliceLabel}
        slice={currentSlice}
        publicApiBaseUrl={publicApiBaseUrl}
        onOpenCode={onOpenCode}
        onOpenCommits={onOpenCommits}
        onOpenChangesets={onOpenChangesets}
        onOpenAgents={onOpenAgents}
        onOpenSettings={() => {}}
      />

      <div className="slice-settings-page-content">
        <div className="slice-settings-shell">
          <header className="slice-settings-hero">
            <div className="slice-settings-hero-copy">
              <p className="slice-settings-kicker">Slice settings</p>
              <h1>{sliceLabel}</h1>
              <p className="slice-settings-hero-text">Current policy, tracked paths, and materialized environment state.</p>
              <code className="slice-settings-slice-id">{sliceId}</code>
            </div>
            <div className="slice-settings-summary-grid" aria-label="Slice settings summary">
              {summaryItems.map((item) => {
                const Icon = item.icon;
                return (
                  <div className="slice-settings-summary-card" key={item.label}>
                    <span className="slice-settings-summary-icon" aria-hidden="true">
                      <Icon size={16} />
                    </span>
                    <span className="slice-settings-summary-label">{item.label}</span>
                    <strong>{item.value}</strong>
                  </div>
                );
              })}
            </div>
          </header>

          <SliceSettings
            sliceId={sliceId}
            sliceName={sliceLabel}
            folderMounts={folderMounts}
            onFolderMountsChange={setFolderMounts}
            initialSettingsData={initialSettingsData}
            showHeader={false}
          />
        </div>
      </div>
    </section>
  );
}
