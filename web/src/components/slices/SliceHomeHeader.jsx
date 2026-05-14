import { Plus } from 'lucide-react';
import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';

export function SliceHomeHeader({ onCreate }) {
  return (
    <div className="slice-home-header">
      <div>
        <Badge variant="secondary" className="eyebrow">Slices</Badge>
        <h1>Your slices</h1>
        <p>Open the root, your home slice, or a focused custom slice to inspect files, commits, and changesets.</p>
      </div>
      <div className="slice-home-actions">
        <Button
          type="button"
          className="slice-create-button"
          onClick={onCreate}
          data-testid="slice-create-open"
        >
          <Plus size={16} aria-hidden="true" />
          Create slice
        </Button>
      </div>
    </div>
  );
}
