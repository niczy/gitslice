import {
  FileText,
  Folder,
} from 'lucide-react';

import {
  getEntryDisplayPath,
  getEntryName,
  sortEntriesByTypeAndName,
} from '../../features/browser/browserModel.js';
import { filterVisibleTreeEntries } from '../../features/browser/browserTreeOperations.js';
import { formatBytes } from '../../utils/format.js';
import { normalizeEntryType } from '../../utils/normalize.js';

export default function RepoFolderPreview({
  hasSelectedDirectoryEntries,
  isLoading,
  onEntryClick,
  selectedDirectoryEntries,
  selectedDirectoryLabel,
  selectedDirectoryPath,
  visibleEntryError,
}) {
  const sortedEntries = sortEntriesByTypeAndName(filterVisibleTreeEntries(selectedDirectoryEntries));

  return (
    <div className="folder-preview" data-testid="folder-preview">
      <div className="folder-preview-header">
        <div>
          <h3>{selectedDirectoryLabel}</h3>
          <span>{selectedDirectoryPath ? `/${selectedDirectoryPath}` : 'Slice root'}</span>
        </div>
        <span className="folder-preview-count">
          {hasSelectedDirectoryEntries ? `${sortedEntries.length} item${sortedEntries.length === 1 ? '' : 's'}` : 'Loading'}
        </span>
      </div>
      {!hasSelectedDirectoryEntries && isLoading && (
        <div className="folder-preview-loading" role="status" aria-live="polite">
          <span className="file-loading-spinner" aria-hidden="true" />
          <span>Loading folder...</span>
        </div>
      )}
      {!hasSelectedDirectoryEntries && !isLoading && visibleEntryError && <div className="panel-error">{visibleEntryError}</div>}
      {hasSelectedDirectoryEntries && sortedEntries.length === 0 && (
        <div className="panel-empty">This folder is empty.</div>
      )}
      {hasSelectedDirectoryEntries && sortedEntries.length > 0 && (
        <ul className="folder-preview-list">
          {sortedEntries.map((entry) => {
            const entryKind = normalizeEntryType(entry.type);
            const entryLabel = getEntryName(entry);
            return (
              <li key={entry.path}>
                <button
                  type="button"
                  className={`folder-preview-entry ${entryKind}`}
                  onClick={() => onEntryClick(entry)}
                  title={getEntryDisplayPath(entry)}
                >
                  <span className="folder-preview-entry-icon" aria-hidden="true">
                    {entryKind === 'directory' ? <Folder size={17} /> : <FileText size={16} />}
                  </span>
                  <span className="folder-preview-entry-name">{entryLabel}</span>
                  <span className="folder-preview-entry-meta">
                    {entryKind === 'directory' ? 'Folder' : formatBytes(entry.size)}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
