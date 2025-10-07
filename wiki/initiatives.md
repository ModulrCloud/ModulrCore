# Location for public devs intentions, todos, etc.


#### 1. Add a separate thread for async ALRP grabbing

###### Problem
Currently once node become a leader it starts the process of ALRP grabbing for each previous leader in leaders sequence in this epoch (or untill leader who created at least one approved block)

###### Solution
Add a separate independent thread which will listen the network and grab ALRPs immediately and in async way

#### 2. Get rid of AEFP necessary when network tries to rotate the epoch

###### Problem

Quorum members generate AEFPs in a separate independent thread once epoch time is out. Another thread finds this AEFPs and include it to blocks of the next epoch to let us recover the execution thread and have a 100% guarantee of latest block in epoch `N` from the first block in epoch `N+1`

###### Solution
We can ignore it and recover execution thread sequence later, in async way. So quorum might be immediately rotated and network will move to the next epoch immediately

#### 3. Architecture "Plan B" - extra threads, 100% stability, no need in awaits for proofs

Blueprints, in next versions