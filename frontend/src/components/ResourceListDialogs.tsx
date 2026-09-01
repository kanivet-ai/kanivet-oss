import TaintDialog from './dialogs/TaintDialog';
import DrainDialog from './dialogs/DrainDialog';
import ScaleDialog from './dialogs/ScaleDialog';
import DrainResultsDialog from './dialogs/DrainResultsDialog';
import DeleteConfirmationDialog from './dialogs/DeleteConfirmationDialog';
import RemoveFinalizersDialog from './dialogs/RemoveFinalizersDialog';
import ActionMenu from './common/ActionMenu';
import TabContextMenu from './common/TabContextMenu';
import { getAvailableActions } from '../utils/resourceActions';

interface ResourceListDialogsProps {
  showDeleteConfirm: boolean;
  setShowDeleteConfirm: (show: boolean) => void;
  selectedResourcesCount: number;
  onConfirmDelete: () => void;
  showRemoveFinalizersConfirm: boolean;
  setShowRemoveFinalizersConfirm: (show: boolean) => void;
  onConfirmRemoveFinalizers: () => void;
  scaleDialog: { item: any; currentReplicas: number } | null;
  setScaleDialog: (dialog: { item: any; currentReplicas: number } | null) => void;
  isScaling: boolean;
  onConfirmScale: (replicas: number) => void;
  taintDialog: { item: any } | null;
  setTaintDialog: (dialog: { item: any } | null) => void;
  isTainting: boolean;
  onConfirmTaint: (key: string, value: string, effect: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute') => void;
  drainDialog: { item: any } | null;
  setDrainDialog: (dialog: { item: any } | null) => void;
  drainOptions: {
    ignoreDaemonsets: boolean;
    deleteEmptyDir: boolean;
    gracePeriod: number;
  };
  setDrainOptions: (options: any) => void;
  isDraining: boolean;
  onConfirmDrain: () => void;
  drainResults: any;
  setDrainResults: (results: any) => void;
  actionMenu: { item: any; x: number; y: number } | null;
  setActionMenu: (menu: { item: any; x: number; y: number } | null) => void;
  selectedNode: any;
  onActionSelect: (action: string, item: any) => void;
  tabContextMenu: { x: number; y: number; tab: any; isDetailTab: boolean; isBottomTab: boolean; isResourceListTab: boolean } | null;
  setTabContextMenu: (menu: any) => void;
  onTabContextAction: (action: string) => void;
  onSplitPane: (direction: 'up' | 'down' | 'left' | 'right') => void;
}

export default function ResourceListDialogs({
  showDeleteConfirm,
  setShowDeleteConfirm,
  selectedResourcesCount,
  onConfirmDelete,
  showRemoveFinalizersConfirm,
  setShowRemoveFinalizersConfirm,
  onConfirmRemoveFinalizers,
  scaleDialog,
  setScaleDialog,
  isScaling,
  onConfirmScale,
  taintDialog,
  setTaintDialog,
  isTainting,
  onConfirmTaint,
  drainDialog,
  setDrainDialog,
  drainOptions,
  setDrainOptions,
  isDraining,
  onConfirmDrain,
  drainResults,
  setDrainResults,
  actionMenu,
  setActionMenu,
  selectedNode,
  onActionSelect,
  tabContextMenu,
  setTabContextMenu,
  onTabContextAction,
  onSplitPane,
}: ResourceListDialogsProps) {
  return (
    <>
      <DeleteConfirmationDialog
        isOpen={showDeleteConfirm}
        resourceCount={selectedResourcesCount}
        isDeleting={false}
        onConfirm={onConfirmDelete}
        onCancel={() => setShowDeleteConfirm(false)}
      />
      <RemoveFinalizersDialog
        isOpen={showRemoveFinalizersConfirm}
        resourceCount={selectedResourcesCount}
        isRemoving={false}
        onConfirm={onConfirmRemoveFinalizers}
        onCancel={() => setShowRemoveFinalizersConfirm(false)}
      />
      {scaleDialog && (
        <ScaleDialog
          isOpen={!!scaleDialog}
          item={scaleDialog.item}
          currentReplicas={scaleDialog.currentReplicas}
          isScaling={isScaling}
          onConfirm={onConfirmScale}
          onCancel={() => setScaleDialog(null)}
        />
      )}
      {taintDialog && (
        <TaintDialog
          item={taintDialog.item}
          onConfirm={onConfirmTaint}
          onClose={() => setTaintDialog(null)}
          isLoading={isTainting}
        />
      )}
      {drainDialog && (
        <DrainDialog
          isOpen={!!drainDialog}
          item={drainDialog.item}
          drainOptions={{
            ignoreDaemonsets: drainOptions.ignoreDaemonsets,
            deleteEmptyDirData: drainOptions.deleteEmptyDir,
            force: false,
            gracePeriod: drainOptions.gracePeriod,
          }}
          isDraining={isDraining}
          onOptionChange={(key, value) => {
            if (key === 'deleteEmptyDirData') {
              setDrainOptions({ ...drainOptions, deleteEmptyDir: value as boolean });
            } else if (key === 'gracePeriod') {
              setDrainOptions({ ...drainOptions, gracePeriod: value as number });
            } else {
              setDrainOptions({ ...drainOptions, [key]: value });
            }
          }}
          onConfirm={onConfirmDrain}
          onCancel={() => setDrainDialog(null)}
        />
      )}
      <ActionMenu
        isOpen={!!actionMenu}
        x={actionMenu?.x || 0}
        y={actionMenu?.y || 0}
        actions={actionMenu ? getAvailableActions(actionMenu.item, selectedNode) : []}
        onSelect={(action) => onActionSelect(action, actionMenu?.item)}
        onClose={() => setActionMenu(null)}
      />
      {drainResults && (
        <DrainResultsDialog
          results={drainResults}
          onClose={() => setDrainResults(null)}
        />
      )}
      <TabContextMenu
        isOpen={!!tabContextMenu}
        x={tabContextMenu?.x || 0}
        y={tabContextMenu?.y || 0}
        onAction={onTabContextAction}
        onClose={() => setTabContextMenu(null)}
        onSplitPane={onSplitPane}
      />
    </>
  );
}
