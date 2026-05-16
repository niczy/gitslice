import {
  ChevronDown,
  ChevronRight,
  Folder,
  X,
} from 'lucide-react';
import { Button } from '../ui/button.jsx';
import {
  getEntryName,
  pathRelativeToHomeRoot,
  sortDirectoryEntries,
} from './SliceHomeHelpers.js';

export function SliceCreateDialog({
  createError,
  createLoading,
  expandedFolderPaths,
  folderBrowserEntries,
  folderInput,
  folderSelectionError,
  homeRootPath,
  loadingFolderPaths,
  onAddFolder,
  onClose,
  onDescriptionChange,
  onFolderInputChange,
  onFolderInputSubmit,
  onNameChange,
  onRemoveFolder,
  onSubmit,
  onToggleFolderExpansion,
  selectedFolders,
  sliceDescription,
  sliceName,
}) {
  const renderFolderTree = (path = '', depth = 0) => {
    const entries = sortDirectoryEntries(folderBrowserEntries[path] || []);
    if (!entries.length && loadingFolderPaths[path]) {
      return (
        <div className="slice-create-folder-loading" role="status">
          Loading folders...
        </div>
      );
    }
    if (!entries.length) {
      return null;
    }

    return (
      <ul className="slice-create-folder-tree">
        {entries.map((entry) => {
          const folderPath = pathRelativeToHomeRoot(homeRootPath, entry.path);
          if (!folderPath) {
            return null;
          }
          const entryName = getEntryName(entry);
          const isExpanded = expandedFolderPaths.includes(folderPath);
          const isSelected = selectedFolders.includes(folderPath);
          const isCovered = selectedFolders.some((selected) => folderPath.startsWith(`${selected}/`));
          const showFolderPath = folderPath !== entryName;
          return (
            <li key={folderPath}>
              <div
                className={`slice-create-folder-row${isSelected ? ' selected' : ''}${isCovered ? ' covered' : ''}`}
                style={{ '--folder-depth': depth }}
              >
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="slice-create-folder-toggle"
                  onClick={() => onToggleFolderExpansion(folderPath)}
                  aria-label={isExpanded ? `Collapse ${folderPath}` : `Expand ${folderPath}`}
                >
                  {isExpanded ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight size={14} aria-hidden="true" />}
                </Button>
                <button
                  type="button"
                  className={`slice-create-folder-option${showFolderPath ? '' : ' compact'}`}
                  onClick={() => onAddFolder(folderPath)}
                  title={folderPath}
                  data-testid="slice-create-folder-option"
                >
                  <Folder size={15} aria-hidden="true" />
                  <span>{entryName}</span>
                  {showFolderPath && <small>{folderPath}</small>}
                </button>
              </div>
              {isExpanded && renderFolderTree(folderPath, depth + 1)}
            </li>
          );
        })}
      </ul>
    );
  };

  return (
    <div className="slice-create-backdrop" role="presentation" onClick={onClose}>
      <form
        className="slice-create-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Create slice"
        onSubmit={onSubmit}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="slice-create-header">
          <div>
            <h2>Create slice</h2>
            <p>Select the home folders this slice should track.</p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-create-close"
            onClick={onClose}
            aria-label="Close create slice dialog"
          >
            <X size={16} aria-hidden="true" />
          </Button>
        </div>
        <label className="slice-create-field">
          <span>Name</span>
          <input
            type="text"
            value={sliceName}
            onChange={(event) => onNameChange(event.target.value)}
            placeholder="Feature slice"
            data-testid="slice-create-name"
            autoFocus
          />
        </label>
        <label className="slice-create-field">
          <span>Description</span>
          <textarea
            value={sliceDescription}
            onChange={(event) => onDescriptionChange(event.target.value)}
            placeholder="What will this slice track?"
            data-testid="slice-create-description"
          />
        </label>
        <section className="slice-create-folders" aria-label="Tracked folders">
          <div className="slice-create-section-heading">
            <span>Tracked folders</span>
            <small>{selectedFolders.length} selected</small>
          </div>
          {selectedFolders.length > 0 ? (
            <ul className="slice-create-selected-folders" data-testid="slice-create-selected-folders">
              {selectedFolders.map((folderPath) => (
                <li key={folderPath}>
                  <span>{folderPath}</span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="slice-create-folder-remove"
                    onClick={() => onRemoveFolder(folderPath)}
                    aria-label={`Remove ${folderPath}`}
                  >
                    <X size={12} aria-hidden="true" />
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <div className="slice-create-folder-empty">Choose at least one folder from your home directory.</div>
          )}
          <div className="slice-create-folder-input">
            <input
              type="text"
              value={folderInput}
              onChange={(event) => onFolderInputChange(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  onFolderInputSubmit();
                }
              }}
              placeholder="apps/web"
              data-testid="slice-create-folder-input"
            />
            <Button type="button" variant="secondary" onClick={onFolderInputSubmit}>
              Add
            </Button>
          </div>
          <div className="slice-create-folder-browser" data-testid="slice-create-folder-browser">
            {renderFolderTree('')}
          </div>
          {folderSelectionError && <div className="panel-error">{folderSelectionError}</div>}
        </section>
        {createError && <div className="panel-error">{createError}</div>}
        <div className="slice-create-actions">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            className="slice-create-button"
            disabled={createLoading}
            data-testid="slice-create-submit"
          >
            {createLoading ? 'Creating...' : 'Create slice'}
          </Button>
        </div>
      </form>
    </div>
  );
}
