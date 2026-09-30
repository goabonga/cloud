// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Route, Routes } from "react-router-dom";

import Layout from "./components/Layout";
import Acls from "./pages/Acls";
import Device from "./pages/Device";
import Login from "./pages/Login";
import Overview from "./pages/Overview";
import Profile from "./pages/Profile";
import Resources from "./pages/Resources";
import Settings from "./pages/Settings";
import Vpcs from "./pages/Vpcs";

export default function App() {
  return (
    <Routes>
      <Route path="login" element={<Login />} />
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        <Route path="vpcs" element={<Vpcs />} />
        <Route path="resources" element={<Resources />} />
        <Route path="acls" element={<Acls />} />
        <Route path="profile" element={<Profile />} />
        <Route path="device" element={<Device />} />
        <Route path="settings" element={<Settings />} />
      </Route>
    </Routes>
  );
}
