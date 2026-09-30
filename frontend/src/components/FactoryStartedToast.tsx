import * as Toast from '@radix-ui/react-toast';

/** A short, self-dismissing confirmation that an action went through. */
export function NoticeToast({ open, onOpenChange, message }: { open: boolean; onOpenChange: (open: boolean) => void; message: string }) {
	return <Toast.Provider swipeDirection="right">
		<Toast.Root className="oc-toast-root" open={open} onOpenChange={onOpenChange}>
			<Toast.Description>{message}</Toast.Description>
		</Toast.Root>
		<Toast.Viewport className="oc-toast-viewport" />
	</Toast.Provider>;
}

export function FactoryStartedToast({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
	return <NoticeToast open={open} onOpenChange={onOpenChange} message="Plan approved. We're starting work." />;
}
