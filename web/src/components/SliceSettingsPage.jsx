import { useEffect, useMemo, useState } from 'react';

import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceSettings from './SliceSettings.jsx';

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
        <SliceSettings
          sliceId={sliceId}
          sliceName={sliceLabel}
          folderMounts={folderMounts}
          onFolderMountsChange={setFolderMounts}
          initialSettingsData={initialSettingsData}
        />
      </div>
    </section>
  );
}
