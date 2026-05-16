import { Folder, Plus, Trash2 } from 'lucide-react';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export function TrackedFoldersCard({
  folderAdding,
  folderError,
  folderRemoving,
  localMounts,
  newFolderPath,
  onAddFolder,
  onFolderKeyDown,
  onNewFolderPathChange,
  onRemoveFolder,
}) {
  return (
    <Card className="slice-settings-card slice-settings-card--folders">
      <CardContent className="pt-6">
        <div className="slice-settings-card-header">
          <div>
            <h4>Tracked folders</h4>
            <p>Folders from the parent slice that this custom slice tracks.</p>
          </div>
        </div>

        {folderError && <div className="panel-error">{folderError}</div>}

        {localMounts.length === 0 && (
          <div className="panel-empty">No tracked folders configured.</div>
        )}

        {localMounts.length > 0 && (
          <div className="tracked-folders-list">
            {localMounts.map((mount) => {
              const mountSource = mount?.source_path || mount?.sourcePath || '';
              const mountAlias = mount?.alias || '';
              return (
                <div key={mountSource} className="tracked-folder-row" data-testid="tracked-folder-row">
                  <div className="tracked-folder-info">
                    <Folder size={14} className="tracked-folder-icon" />
                    <span className="tracked-folder-source">{mountSource}</span>
                    {mountAlias && mountAlias !== mountSource.split('/').pop() && (
                      <span className="tracked-folder-alias">→ {mountAlias}</span>
                    )}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    disabled={folderRemoving === mountSource}
                    onClick={() => onRemoveFolder(mountSource)}
                    data-testid="remove-tracked-folder"
                    title={`Remove ${mountSource}`}
                  >
                    <Trash2 size={14} aria-hidden="true" />
                  </Button>
                </div>
              );
            })}
          </div>
        )}

        <div className="tracked-folder-add">
          <div className="tracked-folder-input-group">
            <input
              type="text"
              className="tracked-folder-input"
              placeholder="src/components"
              value={newFolderPath}
              onChange={(event) => onNewFolderPathChange(event.target.value)}
              onKeyDown={onFolderKeyDown}
              data-testid="tracked-folder-input"
            />
            <Button
              type="button"
              size="sm"
              disabled={folderAdding || !newFolderPath.trim()}
              onClick={onAddFolder}
              data-testid="add-tracked-folder"
            >
              <Plus size={14} aria-hidden="true" />
              {folderAdding ? 'Adding…' : 'Add folder'}
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
