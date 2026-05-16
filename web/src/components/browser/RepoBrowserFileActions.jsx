import {
  Check,
  Edit3,
  ExternalLink,
  FileText,
  History,
  X,
} from 'lucide-react';

import { Button } from '../ui/button.jsx';

export default function RepoBrowserFileActions({
  canEdit = true,
  isCommittingFileEdit = false,
  isEditingFile,
  onActionDone,
  onCancelEdit,
  onCommitEdit,
  onOpenRawFile,
  onShowEdit,
  onToggleHistory,
  selectedFile,
  showHistory,
}) {
  if (!selectedFile) {
    return null;
  }

  return (
    <>
      {!showHistory && canEdit && (
        <>
          <Button
            type="button"
            variant="secondary"
            className={`history-toggle ${isEditingFile ? 'active' : ''}`}
            disabled={isCommittingFileEdit}
            onClick={() => {
              if (isEditingFile) {
                onCancelEdit();
              } else {
                onShowEdit();
              }
              onActionDone?.();
            }}
          >
            {isEditingFile ? <X size={15} aria-hidden="true" /> : <Edit3 size={15} aria-hidden="true" />}
            {isEditingFile ? 'Cancel' : 'Edit'}
          </Button>
          {isEditingFile && (
            <Button
              type="button"
              variant="default"
              className="history-toggle browser-commit-button"
              disabled={isCommittingFileEdit}
              onClick={async () => {
                await onCommitEdit?.();
                onActionDone?.();
              }}
            >
              <Check size={15} aria-hidden="true" />
              {isCommittingFileEdit ? 'Committing...' : 'Commit Changes'}
            </Button>
          )}
        </>
      )}
      <Button
        type="button"
        variant="secondary"
        className={`history-toggle ${showHistory ? 'active' : ''}`}
        disabled={isCommittingFileEdit}
        onClick={() => {
          onToggleHistory();
          onActionDone?.();
        }}
        data-testid="history-toggle"
        title={showHistory ? 'Show file content' : 'Show commit history'}
      >
        {showHistory ? <FileText size={15} aria-hidden="true" /> : <History size={15} aria-hidden="true" />}
        {showHistory ? 'Content' : 'History'}
      </Button>
      {!showHistory && !isEditingFile && (
        <Button
          type="button"
          variant="secondary"
          className="history-toggle"
          onClick={() => {
            onOpenRawFile();
            onActionDone?.();
          }}
          title="Open raw file"
        >
          <ExternalLink size={15} aria-hidden="true" />
          Raw
        </Button>
      )}
    </>
  );
}
