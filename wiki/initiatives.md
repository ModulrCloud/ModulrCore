# Location for public devs intentions, todos, etc.


#### 1. Add a separate thread for async ALRP grabbing

###### Problem
Currently once node become a leader it starts the process of ALRP grabbing for each previous leader in leaders sequence in this epoch (or untill leader who created at least one approved block)

###### Solution
Add a separate independent thread which will listen the network and grab ALRPs immediately and in async way