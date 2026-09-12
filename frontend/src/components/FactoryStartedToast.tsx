import * as Toast from '@radix-ui/react-toast';

export function FactoryStartedToast({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
	return <Toast.Provider swipeDirection="right">
		<Toast.Root className="oc-toast-root" open={open} onOpenChange={onOpenChange}>
			<Toast.Description>Plan approved. We're starting work.</Toast.Description>
		</Toast.Root>
		<Toast.Viewport className="oc-toast-viewport" />
	</Toast.Provider>;
}
