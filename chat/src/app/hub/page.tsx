import {Suspense} from "react";
import {HubView} from "@/components/hub-view";

export default function HubPage() {
  return (
    <Suspense fallback={<div className="p-4 text-center text-sm">Loading the hub...</div>}>
      <HubView />
    </Suspense>
  );
}
