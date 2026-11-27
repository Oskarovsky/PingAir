use chrono::{DateTime, Utc};
use std::collections::VecDeque;
use std::sync::{Arc, Mutex};

const CAP: usize = 200;

#[derive(Clone, Default)]
pub struct EventLog {
    inner: Arc<Mutex<VecDeque<(DateTime<Utc>, String)>>>,
}

impl EventLog {
    pub fn push(&self, msg: impl Into<String>) {
        let mut g = self.inner.lock().unwrap();
        g.push_back((Utc::now(), msg.into()));
        while g.len() > CAP {
            g.pop_front();
        }
    }

    pub fn snapshot(&self) -> Vec<(DateTime<Utc>, String)> {
        self.inner.lock().unwrap().iter().cloned().rev().collect()
    }
}
