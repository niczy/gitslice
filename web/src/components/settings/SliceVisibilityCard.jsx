import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';
import {
  visibilityLabel,
  visibilityTone,
} from './SliceSettingsHelpers.js';

export function SliceVisibilityCard({
  onPropagationModeChange,
  onSaveVisibility,
  slicePropagationMode,
  sliceVisibility,
  sliceVisibilityError,
  sliceVisibilityLoading,
  sliceVisibilitySaving,
  sliceVisibilitySuccess,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="pt-6">
        <div className="slice-settings-card-header">
          <div>
            <h4>Slice visibility</h4>
            <p>Private by default. Making the slice public allows anonymous readers to browse this slice.</p>
          </div>
          <Badge
            variant="outline"
            className={`visibility-badge visibility-badge--${visibilityTone(sliceVisibility)}`}
            data-testid="slice-visibility-status"
          >
            {visibilityLabel(sliceVisibility)}
          </Badge>
        </div>

        {sliceVisibilityLoading && <div className="panel-empty">Loading slice visibility…</div>}
        {!sliceVisibilityLoading && sliceVisibilityError && <div className="panel-error">{sliceVisibilityError}</div>}
        {!sliceVisibilityLoading && sliceVisibilitySuccess && <div className="panel-success">{sliceVisibilitySuccess}</div>}

        {!sliceVisibilityLoading && !sliceVisibilityError && (
          <div className="visibility-stack" data-testid="slice-visibility-panel">
            <div className="visibility-controls">
              <label className="visibility-field">
                <span>Path propagation when making the slice public</span>
                <select
                  value={slicePropagationMode}
                  onChange={(event) => onPropagationModeChange(event.target.value)}
                  data-testid="slice-visibility-propagation"
                >
                  <option value="unchanged">Leave existing path rules unchanged</option>
                  <option value="public">Mark current slice paths public</option>
                  <option value="private">Mark current slice paths private</option>
                </select>
              </label>
              <div className="visibility-actions">
                <Button
                  type="button"
                  variant={sliceVisibility === 'private' ? 'secondary' : 'outline'}
                  disabled={sliceVisibilitySaving}
                  onClick={() => onSaveVisibility('private')}
                  data-testid="slice-visibility-set-private"
                >
                  {sliceVisibilitySaving && sliceVisibility === 'private' ? 'Saving…' : 'Make private'}
                </Button>
                <Button
                  type="button"
                  variant={sliceVisibility === 'public' ? 'secondary' : 'default'}
                  disabled={sliceVisibilitySaving}
                  onClick={() => onSaveVisibility('public')}
                  data-testid="slice-visibility-set-public"
                >
                  {sliceVisibilitySaving && sliceVisibility === 'public' ? 'Saving…' : 'Make public'}
                </Button>
              </div>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
