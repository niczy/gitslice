import { X } from 'lucide-react';

import SliceSettings from '../SliceSettings.jsx';
import { Button } from '../ui/button.jsx';

export default function RepoBrowserSettingsModal({
  currentSlice,
  currentSliceLabel,
  onClose,
  sliceId,
  viewingSettings,
}) {
  if (!viewingSettings) {
    return null;
  }

  return (
    <div
      className="slice-settings-modal-backdrop"
      role="presentation"
      onClick={onClose}
    >
      <div
        className="slice-settings-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Slice settings"
        onClick={(event) => event.stopPropagation()}
      >
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="slice-settings-modal-close"
          onClick={onClose}
          aria-label="Close slice settings"
          title="Close slice settings"
        >
          <X size={17} aria-hidden="true" />
        </Button>
        <SliceSettings
          sliceId={sliceId}
          sliceName={currentSliceLabel}
          folderMounts={currentSlice?.folder_mounts}
          onFolderMountsChange={(updatedMounts) => {
            if (currentSlice) {
              currentSlice.folder_mounts = updatedMounts;
            }
          }}
        />
      </div>
    </div>
  );
}
