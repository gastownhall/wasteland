import * as Sentry from "@sentry/react";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { BrowseList } from "./components/BrowseList";
import { ConnectPage } from "./components/ConnectPage";
import { Dashboard } from "./components/Dashboard";
import { DetailView } from "./components/DetailView";
import { Layout } from "./components/Layout";
import { ProfileSearch } from "./components/ProfileSearch";
import { ProfileView } from "./components/ProfileView";
import { Scoreboard } from "./components/Scoreboard";
import { Settings } from "./components/Settings";
import { WastelandProvider } from "./context/WastelandContext";

export function App() {
  return (
    <Sentry.ErrorBoundary fallback={<p>Something went wrong.</p>}>
      <WastelandProvider>
        <BrowserRouter>
          <Routes>
            <Route element={<Layout />}>
              <Route path="/" element={<BrowseList />} />
              <Route path="/wanted/:id" element={<DetailView />} />
              <Route path="/me" element={<Dashboard />} />
              <Route path="/profile" element={<ProfileSearch />} />
              <Route path="/profile/:handle" element={<ProfileView />} />
              <Route path="/scoreboard" element={<Scoreboard />} />
              <Route path="/settings" element={<Settings />} />
              <Route path="/connect" element={<ConnectPage />} />
              <Route path="/join" element={<ConnectPage />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </WastelandProvider>
    </Sentry.ErrorBoundary>
  );
}
